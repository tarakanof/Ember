package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tarakanof/ember/internal/devices"
)

const (
	claimPollInterval = 5 * time.Second
	claimBodyMax      = 4096
)

func secondsUntil(t, now time.Time) int {
	return int(math.Ceil(t.Sub(now).Seconds()))
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
	r.Body = http.MaxBytesReader(w, r.Body, claimBodyMax)
	var req struct {
		HwID     string `json:"hw_id"`
		FW       string `json:"fw"`
		ClaimKey string `json:"claim_key"`
	}
	if !a.decodeOrReject(w, r, &req, false) {
		return
	}
	hwID, err := devices.NormalizeHwID(req.HwID)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if utf8.RuneCountInString(req.FW) > 32 || !utf8.ValidString(req.FW) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: fw must be at most 32 characters", devices.ErrBody))
		return
	}
	if !devices.ValidClaimKey(req.ClaimKey) {
		a.writeDeviceError(w, r, fmt.Errorf("%w: claim_key must be 32 bytes as 43 base64url characters", devices.ErrBody))
		return
	}
	ip := clientIP(r)
	now := a.claims.Now()
	if until := a.devices.KnobLiveUntil(hwID); now.Before(until) {
		a.logger.InfoContext(r.Context(), "device claim refused", "reason", "already_paired", "hw_id", hwID, "ip", ip)
		setRetryAfter(w, until.Sub(now))
		writeError(w, http.StatusConflict, devices.ErrAlreadyPaired)
		return
	}
	c, reused, err := a.claims.Open(hwID, req.FW, ip, req.ClaimKey)
	var full devices.ClaimsFullError
	if errors.As(err, &full) {
		a.logger.InfoContext(r.Context(), "device claim refused", "reason", "full", "hw_id", hwID, "ip", ip)
		setRetryAfter(w, full.RetryAfter)
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	status := http.StatusCreated
	if reused {
		status = http.StatusOK
	} else {
		a.logger.InfoContext(r.Context(), "device claim created", "claim_id", c.ID, "hw_id", hwID, "ip", ip)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, claimOpened{
		ClaimID:    c.ID,
		Code:       c.Code,
		ExpiresInS: max(secondsUntil(c.ExpiresAt, a.claims.Now()), 1),
		PollS:      int(claimPollInterval / time.Second),
	})
}

func setRetryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(d.Seconds())), 1)))
}

func (a *App) handleDeviceClaimPoll(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, claimBodyMax)
	var req struct {
		ClaimID  string `json:"claim_id"`
		ClaimKey string `json:"claim_key"`
	}
	if !a.decodeOrReject(w, r, &req, false) {
		return
	}
	c, err := a.claims.Poll(req.ClaimID, req.ClaimKey, a.devices.TokenCurrent)
	if err != nil {
		writeError(w, http.StatusNotFound, devices.ErrClaimNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if c.Approved == nil {
		writeJSON(w, http.StatusAccepted, claimPollReply{State: "pending", ExpiresInS: max(secondsUntil(c.ExpiresAt, a.claims.Now()), 1)})
		return
	}
	writeJSON(w, http.StatusOK, claimPollReply{
		State:    "approved",
		DeviceID: c.Approved.View.ID,
		Name:     c.Approved.View.Name,
		Token:    c.Approved.Token,
	})
}

func (a *App) handleDeviceClaimsList(w http.ResponseWriter, r *http.Request) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	claims := a.claims.Pending()
	out := make([]claimView, 0, len(claims))
	for _, c := range claims {
		v := claimView{
			ID:        c.ID,
			HwID:      c.HwID,
			ShortID:   strings.ToUpper(c.HwID[len(c.HwID)-6:]),
			FW:        c.FW,
			IP:        c.IP,
			CreatedAt: c.CreatedAt.UTC().Truncate(time.Second),
			ExpiresAt: c.ExpiresAt.UTC().Truncate(time.Second),
		}
		if id, _ := a.devices.KnobByHw(c.HwID); id != "" {
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
	name, err := devices.NormalizeName(req.Name, false)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	id := r.PathValue("id")
	c, superseded, err := a.claims.Approve(id, req.Code, devices.ClaimMinter{
		LiveUntil: a.devices.KnobLiveUntil,
		Mint: func(hwID string) (devices.View, string, bool, error) {
			return a.devices.Provision(hwID, name)
		},
	}, a.devices.TokenCurrent)
	var wrong devices.WrongCodeError
	var live devices.AlreadyPairedError
	switch {
	case errors.As(err, &wrong):
		a.logger.InfoContext(r.Context(), "device claim code rejected", "claim_id", id, "hw_id", c.HwID, "attempts_left", wrong.Left)
		if wrong.Left == 0 {
			a.logger.InfoContext(r.Context(), "device claim declined", "reason", "attempts", "claim_id", id, "hw_id", c.HwID, "ip", c.IP)
		}
		writeJSON(w, http.StatusBadRequest, claimCodeRejected{Error: wrong.Error(), AttemptsLeft: wrong.Left})
		return
	case errors.As(err, &live):
		a.logger.InfoContext(r.Context(), "device claim declined", "reason", "already_paired", "claim_id", id, "hw_id", c.HwID, "ip", c.IP)
		setRetryAfter(w, live.RetryAfter)
		writeError(w, http.StatusConflict, err)
		return
	case errors.Is(err, devices.ErrClaimNotFound):
		writeError(w, http.StatusNotFound, err)
		return
	case err != nil:
		a.writeDeviceError(w, r, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device claim approved", "claim_id", c.ID, "hw_id", c.HwID, "ip", c.IP,
		"device_id", c.Approved.View.ID, "reprovisioned", !c.Approved.Created)
	for _, o := range superseded {
		a.logger.InfoContext(r.Context(), "device claim declined", "reason", "superseded", "claim_id", o.ID, "hw_id", o.HwID, "ip", o.IP)
	}
	writeJSON(w, http.StatusOK, c.Approved.View)
}

func (a *App) handleDeviceClaimDecline(w http.ResponseWriter, r *http.Request) {
	if !a.requireMasterForClients(w, r) {
		return
	}
	c, err := a.claims.Decline(r.PathValue("id"), a.devices.TokenCurrent)
	if errors.Is(err, devices.ErrClaimApproved) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	a.logger.InfoContext(r.Context(), "device claim declined", "reason", "owner", "claim_id", c.ID, "hw_id", c.HwID, "ip", c.IP)
	w.WriteHeader(http.StatusNoContent)
}
