package producer

import "time"

const KeepaliveInterval = 15 * time.Second

type Repost struct {
	fp string
	at time.Time
}

func (r *Repost) Due(fp string, now time.Time) bool {
	if fp == r.fp && now.Sub(r.at) < KeepaliveInterval {
		return false
	}
	r.fp, r.at = fp, now
	return true
}

func (r *Repost) Posted() bool { return !r.at.IsZero() }

func (r *Repost) Reset() { *r = Repost{} }
