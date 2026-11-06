package devices

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
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ClaimTTL         = 10 * time.Minute
	claimMaxPerIP    = 2
	ClaimMaxTotal    = 16
	ClaimMaxAttempts = 5
	ClaimLiveWindow  = 5 * time.Minute
	ClaimKeyBytes    = 32
	claimIDPrefix    = "c_"
	claimCodeDraws   = math.MaxUint32 / 1_000_000 * 1_000_000
)

var (
	ErrClaimNotFound = errors.New("claim not found")
	ErrClaimsFull    = errors.New("too many pending claims")
	ErrAlreadyPaired = errors.New("already_paired")
	ErrClaimApproved = errors.New("claim_approved")
)

type ClaimsFullError struct{ RetryAfter time.Duration }

func (e ClaimsFullError) Error() string { return ErrClaimsFull.Error() }

func (e ClaimsFullError) Unwrap() error { return ErrClaimsFull }

type AlreadyPairedError struct{ RetryAfter time.Duration }

func (e AlreadyPairedError) Error() string { return ErrAlreadyPaired.Error() }

func (e AlreadyPairedError) Unwrap() error { return ErrAlreadyPaired }

type WrongCodeError struct{ Left int }

func (e WrongCodeError) Error() string { return "wrong_code" }

type Claim struct {
	ID        string
	HwID      string
	FW        string
	IP        string
	source    string
	keyHash   [sha256.Size]byte
	Code      string
	CreatedAt time.Time
	ExpiresAt time.Time
	wrong     int
	Approved  *ClaimApproval
}

type ClaimApproval struct {
	View      View
	Token     string
	tokenHash string
	Created   bool
}

type ClaimRegistry struct {
	Now      func() time.Time
	Rand     io.Reader
	OnExpire func(Claim)

	mu     sync.Mutex
	claims map[string]*Claim
}

func NewClaimRegistry() *ClaimRegistry {
	return &ClaimRegistry{Now: time.Now, Rand: rand.Reader, claims: map[string]*Claim{}}
}

func (r *ClaimRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.claims)
}

func (r *ClaimRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claims = map[string]*Claim{}
}

func (r *ClaimRegistry) pruneLocked(now time.Time) {
	for id, c := range r.claims {
		if !now.Before(c.ExpiresAt) {
			delete(r.claims, id)
			if r.OnExpire != nil {
				r.OnExpire(*c)
			}
		}
	}
}

func (r *ClaimRegistry) Open(hwID, fw, ip, key string) (Claim, bool, error) {
	h := sha256.Sum256([]byte(key))
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	r.pruneLocked(now)
	for _, c := range r.sortedLocked() {
		if c.HwID == hwID && subtle.ConstantTimeCompare(h[:], c.keyHash[:]) == 1 {
			return *c, true, nil
		}
	}
	source := claimSource(ip)
	var fromSource []*Claim
	for _, c := range r.claims {
		if c.source == source {
			fromSource = append(fromSource, c)
		}
	}
	if len(fromSource) >= claimMaxPerIP {
		return Claim{}, false, ClaimsFullError{RetryAfter: firstExpiry(fromSource).Sub(now)}
	}
	if len(r.claims) >= ClaimMaxTotal {
		return Claim{}, false, ClaimsFullError{RetryAfter: firstExpiry(r.sortedLocked()).Sub(now)}
	}
	c := &Claim{
		ID:        r.newIDLocked(),
		HwID:      hwID,
		FW:        fw,
		IP:        ip,
		source:    source,
		keyHash:   h,
		Code:      r.newCodeLocked(),
		CreatedAt: now,
		ExpiresAt: now.Add(ClaimTTL),
	}
	r.claims[c.ID] = c
	return *c, false, nil
}

func firstExpiry(claims []*Claim) time.Time {
	first := claims[0].ExpiresAt
	for _, c := range claims[1:] {
		if c.ExpiresAt.Before(first) {
			first = c.ExpiresAt
		}
	}
	return first
}

func (r *ClaimRegistry) draw() []byte {
	b := make([]byte, 4)
	if _, err := io.ReadFull(r.Rand, b); err != nil {
		panic(fmt.Sprintf("claim randomness failed: %v", err))
	}
	return b
}

func (r *ClaimRegistry) newIDLocked() string {
	for {
		b := r.draw()
		id := claimIDPrefix + hex.EncodeToString(b)
		if r.claims[id] == nil {
			return id
		}
	}
}

func (r *ClaimRegistry) newCodeLocked() string {
	for {
		n := binary.BigEndian.Uint32(r.draw())
		if n >= claimCodeDraws {
			continue
		}
		code := fmt.Sprintf("%06d", n%1_000_000)
		taken := false
		for _, c := range r.claims {
			taken = taken || c.Code == code
		}
		if !taken {
			return code
		}
	}
}

func (r *ClaimRegistry) sortedLocked() []*Claim {
	out := make([]*Claim, 0, len(r.claims))
	for _, c := range r.claims {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *Claim) int {
		if n := a.CreatedAt.Compare(b.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (r *ClaimRegistry) Poll(id, key string, current func(deviceID, tokenHash string) bool) (Claim, error) {
	h := sha256.Sum256([]byte(key))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.Now())
	c := r.claims[id]
	if c == nil || subtle.ConstantTimeCompare(h[:], c.keyHash[:]) != 1 {
		return Claim{}, ErrClaimNotFound
	}
	if c.Approved != nil && !current(c.Approved.View.ID, c.Approved.tokenHash) {
		delete(r.claims, id)
		return Claim{}, ErrClaimNotFound
	}
	return *c, nil
}

func (r *ClaimRegistry) Pending() []Claim {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.Now())
	var out []Claim
	for _, c := range r.sortedLocked() {
		if c.Approved == nil {
			out = append(out, *c)
		}
	}
	return out
}

type ClaimMinter struct {
	LiveUntil func(hwID string) time.Time
	Mint      func(hwID string) (View, string, bool, error)
}

func (r *ClaimRegistry) Approve(id, code string, m ClaimMinter, current func(deviceID, tokenHash string) bool) (Claim, []Claim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	r.pruneLocked(now)
	c := r.claims[id]
	if c == nil {
		return Claim{}, nil, ErrClaimNotFound
	}
	if c.Approved != nil && !current(c.Approved.View.ID, c.Approved.tokenHash) {
		delete(r.claims, id)
		return Claim{}, nil, ErrClaimNotFound
	}
	right := subtle.ConstantTimeCompare([]byte(strings.ReplaceAll(code, " ", "")), []byte(c.Code)) == 1
	if c.Approved != nil {
		if !right {
			return *c, nil, WrongCodeError{Left: ClaimMaxAttempts - c.wrong}
		}
		return *c, nil, nil
	}
	if !right {
		c.wrong++
		left := ClaimMaxAttempts - c.wrong
		if left <= 0 {
			delete(r.claims, id)
		}
		return *c, nil, WrongCodeError{Left: max(left, 0)}
	}
	if until := m.LiveUntil(c.HwID); now.Before(until) {
		delete(r.claims, id)
		return *c, nil, AlreadyPairedError{RetryAfter: until.Sub(now)}
	}
	view, token, created, err := m.Mint(c.HwID)
	if err != nil {
		return Claim{}, nil, err
	}
	c.Approved = &ClaimApproval{View: view, Token: token, tokenHash: TokenHash(token), Created: created}
	c.ExpiresAt = now.Add(ClaimTTL)
	var superseded []Claim
	for _, o := range r.sortedLocked() {
		if o.ID != c.ID && o.HwID == c.HwID {
			delete(r.claims, o.ID)
			superseded = append(superseded, *o)
		}
	}
	return *c, superseded, nil
}

func (r *ClaimRegistry) Decline(id string, current func(deviceID, tokenHash string) bool) (Claim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.Now())
	c := r.claims[id]
	if c == nil {
		return Claim{}, ErrClaimNotFound
	}
	if c.Approved != nil {
		if !current(c.Approved.View.ID, c.Approved.tokenHash) {
			delete(r.claims, id)
			return Claim{}, ErrClaimNotFound
		}
		return *c, ErrClaimApproved
	}
	delete(r.claims, id)
	return *c, nil
}

func (r *ClaimRegistry) Collect(token string) (Claim, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.claims) == 0 {
		return Claim{}, false
	}
	h := []byte(TokenHash(token))
	for id, c := range r.claims {
		if c.Approved != nil && subtle.ConstantTimeCompare(h, []byte(c.Approved.tokenHash)) == 1 {
			delete(r.claims, id)
			return *c, true
		}
	}
	return Claim{}, false
}

func claimSource(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

func ValidClaimKey(key string) bool {
	if len(key) != base64.RawURLEncoding.EncodedLen(ClaimKeyBytes) {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(key)
	return err == nil && len(b) == ClaimKeyBytes
}

func (r *Registry) KnobLiveUntil(hwID string) time.Time {
	if _, seen := r.KnobByHw(hwID); seen != nil {
		return seen.Add(ClaimLiveWindow)
	}
	return time.Time{}
}

func (r *Registry) TokenCurrent(id, hash string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	return d != nil && subtle.ConstantTimeCompare([]byte(d.TokenSHA256), []byte(hash)) == 1
}

func (r *Registry) KnobByHw(hwID string) (string, *time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.state.Devices {
		if d.Kind == KindKnob && d.HwID == hwID {
			if d.LastCheckin == nil {
				return d.ID, nil
			}
			seen := d.LastCheckin.SeenAt
			return d.ID, &seen
		}
	}
	return "", nil
}
