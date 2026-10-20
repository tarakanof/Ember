package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	claimTTL          = 10 * time.Minute
	claimPollInterval = 5 * time.Second
	claimMaxPerIP     = 2
	claimMaxTotal     = 16
	claimMaxAttempts  = 5
	claimLiveWindow   = 5 * time.Minute
	claimKeyBytes     = 32
	claimIDPrefix     = "c_"
	claimCodeDraws    = math.MaxUint32 / 1_000_000 * 1_000_000
)

var (
	errClaimNotFound = errors.New("claim not found")
	errClaimsFull    = errors.New("too many pending claims")
	errAlreadyPaired = errors.New("already_paired")
)

type wrongCodeError struct{ left int }

func (e wrongCodeError) Error() string { return "wrong_code" }

type deviceClaim struct {
	id        string
	hwID      string
	fw        string
	ip        string
	keyHash   [sha256.Size]byte
	code      string
	createdAt time.Time
	expiresAt time.Time
	wrong     int
	approved  *claimApproval
}

type claimApproval struct {
	view      deviceView
	token     string
	tokenHash string
	created   bool
}

type claimRegistry struct {
	now  func() time.Time
	rand io.Reader
	// Runs under mu: it must not block or call back into the registry.
	onExpire func(deviceClaim)

	mu     sync.Mutex
	claims map[string]*deviceClaim
}

func newClaimRegistry() *claimRegistry {
	return &claimRegistry{now: time.Now, rand: rand.Reader, claims: map[string]*deviceClaim{}}
}

func (r *claimRegistry) pruneLocked(now time.Time) {
	for id, c := range r.claims {
		if !now.Before(c.expiresAt) {
			delete(r.claims, id)
			if r.onExpire != nil {
				r.onExpire(*c)
			}
		}
	}
}

func (r *claimRegistry) open(hwID, fw, ip, key string) (deviceClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	fromIP := 0
	for _, c := range r.claims {
		if c.ip == ip {
			fromIP++
		}
	}
	if len(r.claims) >= claimMaxTotal || fromIP >= claimMaxPerIP {
		return deviceClaim{}, errClaimsFull
	}
	c := &deviceClaim{
		id:        r.newIDLocked(),
		hwID:      hwID,
		fw:        fw,
		ip:        ip,
		keyHash:   sha256.Sum256([]byte(key)),
		code:      r.newCodeLocked(),
		createdAt: now,
		expiresAt: now.Add(claimTTL),
	}
	r.claims[c.id] = c
	return *c, nil
}

func (r *claimRegistry) draw() []byte {
	b := make([]byte, 4)
	if _, err := io.ReadFull(r.rand, b); err != nil {
		panic(fmt.Sprintf("claim randomness failed: %v", err))
	}
	return b
}

func (r *claimRegistry) newIDLocked() string {
	for {
		b := r.draw()
		id := claimIDPrefix + hex.EncodeToString(b)
		if r.claims[id] == nil {
			return id
		}
	}
}

func (r *claimRegistry) newCodeLocked() string {
	for {
		n := binary.BigEndian.Uint32(r.draw())
		if n >= claimCodeDraws {
			continue
		}
		code := fmt.Sprintf("%06d", n%1_000_000)
		taken := false
		for _, c := range r.claims {
			taken = taken || c.code == code
		}
		if !taken {
			return code
		}
	}
}

func (r *claimRegistry) sortedLocked() []*deviceClaim {
	out := make([]*deviceClaim, 0, len(r.claims))
	for _, c := range r.claims {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *deviceClaim) int {
		if n := a.createdAt.Compare(b.createdAt); n != 0 {
			return n
		}
		return strings.Compare(a.id, b.id)
	})
	return out
}

func (r *claimRegistry) poll(id, key string) (deviceClaim, error) {
	h := sha256.Sum256([]byte(key))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	c := r.claims[id]
	if c == nil || subtle.ConstantTimeCompare(h[:], c.keyHash[:]) != 1 {
		return deviceClaim{}, errClaimNotFound
	}
	return *c, nil
}

func (r *claimRegistry) pending() []deviceClaim {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	var out []deviceClaim
	for _, c := range r.sortedLocked() {
		if c.approved == nil {
			out = append(out, *c)
		}
	}
	return out
}

func (r *claimRegistry) approve(id, code string, mint func(hwID string) (deviceView, string, bool, error)) (deviceClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	c := r.claims[id]
	if c == nil {
		return deviceClaim{}, errClaimNotFound
	}
	if subtle.ConstantTimeCompare([]byte(strings.ReplaceAll(code, " ", "")), []byte(c.code)) != 1 {
		c.wrong++
		left := claimMaxAttempts - c.wrong
		if left <= 0 {
			delete(r.claims, id)
		}
		return *c, wrongCodeError{left: max(left, 0)}
	}
	if c.approved != nil {
		return *c, nil
	}
	view, token, created, err := mint(c.hwID)
	if err != nil {
		return deviceClaim{}, err
	}
	c.approved = &claimApproval{view: view, token: token, tokenHash: tokenHash(token), created: created}
	c.expiresAt = now.Add(claimTTL)
	return *c, nil
}

func (r *claimRegistry) decline(id string) (deviceClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	c := r.claims[id]
	if c == nil {
		return deviceClaim{}, errClaimNotFound
	}
	delete(r.claims, id)
	return *c, nil
}

func (r *claimRegistry) collect(token string) (deviceClaim, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.claims) == 0 {
		return deviceClaim{}, false
	}
	h := []byte(tokenHash(token))
	for id, c := range r.claims {
		if c.approved != nil && subtle.ConstantTimeCompare(h, []byte(c.approved.tokenHash)) == 1 {
			delete(r.claims, id)
			return *c, true
		}
	}
	return deviceClaim{}, false
}

func validClaimKey(key string) bool {
	if len(key) != base64.RawURLEncoding.EncodedLen(claimKeyBytes) {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(key)
	return err == nil && len(b) == claimKeyBytes
}

func secondsUntil(t, now time.Time) int {
	return int(math.Ceil(t.Sub(now).Seconds()))
}

func (r *deviceRegistry) knobByHw(hwID string) (string, *time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.state.Devices {
		if d.Kind == deviceKindKnob && d.HwID == hwID {
			if d.LastCheckin == nil {
				return d.ID, nil
			}
			seen := d.LastCheckin.SeenAt
			return d.ID, &seen
		}
	}
	return "", nil
}

type claimOpened struct {
	ClaimID    string `json:"claim_id"`
	Code       string `json:"code"`
	ExpiresInS int    `json:"expires_in_s"`
	PollS      int    `json:"poll_s"`
}

type claimPollReply struct {
	State      string `json:"state"`
	ExpiresInS int    `json:"expires_in_s,omitempty"`
	DeviceID   string `json:"device_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Token      string `json:"token,omitempty"`
}

type claimCodeRejected struct {
	Error        string `json:"error"`
	AttemptsLeft int    `json:"attempts_left"`
}

type claimView struct {
	ID            string    `json:"id"`
	HwID          string    `json:"hw_id"`
	ShortID       string    `json:"short_id"`
	FW            string    `json:"fw"`
	IP            string    `json:"ip"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	KnownDeviceID *string   `json:"known_device_id"`
}

func (a *App) handleDeviceClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HwID     string `json:"hw_id"`
		FW       string `json:"fw"`
		ClaimKey string `json:"claim_key"`
	}
	if !a.decodeOrReject(w, r, &req, false) {
		return
	}
	hwID, err := normalizeHwID(req.HwID)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if utf8.RuneCountInString(req.FW) > 32 || !utf8.ValidString(req.FW) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: fw must be at most 32 characters", errDeviceBody))
		return
	}
	if !validClaimKey(req.ClaimKey) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: claim_key must be 32 bytes as 43 base64url characters", errDeviceBody))
		return
	}
	ip := clientIP(r)
	if _, seen := a.devices.knobByHw(hwID); seen != nil && a.claims.now().Sub(*seen) < claimLiveWindow {
		a.logger.InfoContext(r.Context(), "device claim refused", "reason", "already_paired", "hw_id", hwID, "ip", ip)
		writeError(w, http.StatusConflict, errAlreadyPaired)
		return
	}
	c, err := a.claims.open(hwID, req.FW, ip, req.ClaimKey)
	if err != nil {
		a.logger.InfoContext(r.Context(), "device claim refused", "reason", "full", "hw_id", hwID, "ip", ip)
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device claim created", "claim_id", c.id, "hw_id", hwID, "ip", ip)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, claimOpened{
		ClaimID:    c.id,
		Code:       c.code,
		ExpiresInS: secondsUntil(c.expiresAt, c.createdAt),
		PollS:      int(claimPollInterval / time.Second),
	})
}

func (a *App) handleDeviceClaimPoll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClaimID  string `json:"claim_id"`
		ClaimKey string `json:"claim_key"`
	}
	if !a.decodeOrReject(w, r, &req, false) {
		return
	}
	c, err := a.claims.poll(req.ClaimID, req.ClaimKey)
	if err != nil {
		writeError(w, http.StatusNotFound, errClaimNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if c.approved == nil {
		writeJSON(w, http.StatusAccepted, claimPollReply{State: "pending", ExpiresInS: max(secondsUntil(c.expiresAt, a.claims.now()), 1)})
		return
	}
	writeJSON(w, http.StatusOK, claimPollReply{
		State:    "approved",
		DeviceID: c.approved.view.ID,
		Name:     c.approved.view.Name,
		Token:    c.approved.token,
	})
}

func (a *App) handleDeviceClaimsList(w http.ResponseWriter, r *http.Request) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	claims := a.claims.pending()
	out := make([]claimView, 0, len(claims))
	for _, c := range claims {
		v := claimView{
			ID:        c.id,
			HwID:      c.hwID,
			ShortID:   strings.ToUpper(c.hwID[len(c.hwID)-6:]),
			FW:        c.fw,
			IP:        c.ip,
			CreatedAt: c.createdAt.UTC().Truncate(time.Second),
			ExpiresAt: c.expiresAt.UTC().Truncate(time.Second),
		}
		if id, _ := a.devices.knobByHw(c.hwID); id != "" {
			v.KnownDeviceID = &id
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"claims": out})
}

func (a *App) handleDeviceClaimApprove(w http.ResponseWriter, r *http.Request) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	name, err := normalizeDeviceName(req.Name, false)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	id := r.PathValue("id")
	c, err := a.claims.approve(id, req.Code, func(hwID string) (deviceView, string, bool, error) {
		return a.devices.provision(hwID, name)
	})
	var wrong wrongCodeError
	switch {
	case errors.As(err, &wrong):
		a.logger.InfoContext(r.Context(), "device claim code rejected", "claim_id", id, "hw_id", c.hwID, "attempts_left", wrong.left)
		if wrong.left == 0 {
			a.logger.InfoContext(r.Context(), "device claim declined", "reason", "attempts", "claim_id", id, "hw_id", c.hwID, "ip", c.ip)
		}
		writeJSON(w, http.StatusBadRequest, claimCodeRejected{Error: wrong.Error(), AttemptsLeft: wrong.left})
		return
	case errors.Is(err, errClaimNotFound):
		writeError(w, http.StatusNotFound, err)
		return
	case err != nil:
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device claim approved", "claim_id", c.id, "hw_id", c.hwID, "ip", c.ip,
		"device_id", c.approved.view.ID, "reprovisioned", !c.approved.created)
	writeJSON(w, http.StatusOK, c.approved.view)
}

func (a *App) handleDeviceClaimDecline(w http.ResponseWriter, r *http.Request) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	c, err := a.claims.decline(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device claim declined", "reason", "owner", "claim_id", c.id, "hw_id", c.hwID, "ip", c.ip)
	w.WriteHeader(http.StatusNoContent)
}
