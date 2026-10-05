package producer

import "time"

// KeepaliveInterval is how often a daemon re-POSTs an unchanged session, well
// under the server's staleness reap.
const KeepaliveInterval = 15 * time.Second

// Repost is a daemon's record of the last POST for one item (a session, a
// usage snapshot). The zero value has never posted.
type Repost struct {
	fp string
	at time.Time
}

// Due reports whether an item with fingerprint fp should be POSTed now (it
// changed, or KeepaliveInterval passed) and, if so, records the POST.
func (r *Repost) Due(fp string, now time.Time) bool {
	if fp == r.fp && now.Sub(r.at) < KeepaliveInterval {
		return false
	}
	r.fp, r.at = fp, now
	return true
}

// Posted reports whether Due ever fired since the last Reset.
func (r *Repost) Posted() bool { return !r.at.IsZero() }

// Reset forgets the last POST, so the next Due fires.
func (r *Repost) Reset() { *r = Repost{} }
