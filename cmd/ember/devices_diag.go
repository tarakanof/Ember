package main

import (
	"errors"
	"maps"
	"regexp"
)

const (
	diagTaskNameMax  = 16
	diagStackTaskMax = 32
	coredumpMaxBytes = 128 << 10
)

type deviceDiag struct {
	Boots            int64          `json:"boots,omitempty"`
	Crash            *deviceCrash   `json:"crash,omitempty"`
	HeapInternalMin  int            `json:"heap_internal_min,omitempty"`
	HeapLargestMin   int            `json:"heap_largest_min,omitempty"`
	ResetReason      string         `json:"reset_reason,omitempty"`
	StackFree        map[string]int `json:"stack_free,omitempty"`
	Reboots          int64          `json:"reboots,omitempty"`
	PrevResetReason  string         `json:"prev_reset_reason,omitempty"`
	RebootsSinceSeen int64          `json:"reboots_since_seen,omitempty"`
}

type deviceCrash struct {
	ELF    string `json:"elf,omitempty"`
	ID     string `json:"id,omitempty"`
	Size   int    `json:"size,omitempty"`
	PC     string `json:"pc,omitempty"`
	Reason string `json:"reason,omitempty"`
	Task   string `json:"task,omitempty"`
}

var (
	diagReasonPattern = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)
	diagPCPattern     = regexp.MustCompile(`^0x[0-9a-f]{1,8}$`)
	coredumpIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

func (d deviceDiag) validate() error {
	switch {
	case d.Boots < 0:
		return errors.New("boots must be >= 0")
	case d.HeapInternalMin < 0:
		return errors.New("heap_internal_min must be >= 0")
	case d.HeapLargestMin < 0:
		return errors.New("heap_largest_min must be >= 0")
	case d.ResetReason != "" && !diagReasonPattern.MatchString(d.ResetReason):
		return errors.New("reset_reason must be 1..24 of a-z, 0-9, _")
	case len(d.StackFree) > diagStackTaskMax:
		return errors.New("stack_free must have at most 32 tasks")
	}
	if c := d.Crash; c != nil {
		switch {
		case c.Reason != "" && !diagReasonPattern.MatchString(c.Reason):
			return errors.New("crash.reason must be 1..24 of a-z, 0-9, _")
		case c.PC != "" && !diagPCPattern.MatchString(c.PC):
			return errors.New("crash.pc must be lower-case 0x plus 1..8 hex digits")
		case c.Task != "" && !validDiagTaskName(c.Task):
			return errors.New("crash.task must be 1..16 printable ASCII characters")
		case (c.ID == "") != (c.Size == 0):
			return errors.New("crash.id and crash.size come together")
		case c.ID != "" && !coredumpIDPattern.MatchString(c.ID):
			return errors.New("crash.id must be 8 lower-case hex digits")
		case c.Size < 0 || c.Size > coredumpMaxBytes:
			return errors.New("crash.size must be 1..131072")
		case c.ELF != "" && !firmwareBuildPattern.MatchString(c.ELF):
			return errors.New("crash.elf must be 8 lower-case hex digits")
		}
	}
	for task, free := range d.StackFree {
		if !validDiagTaskName(task) {
			return errors.New("stack_free task names must be 1..16 printable ASCII characters")
		}
		if free < 0 {
			return errors.New("stack_free values must be >= 0")
		}
	}
	return nil
}

func validDiagTaskName(s string) bool {
	if len(s) == 0 || len(s) > diagTaskNameMax {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func (d *deviceDiag) clone() *deviceDiag {
	if d == nil {
		return nil
	}
	c := *d
	if d.Crash != nil {
		crash := *d.Crash
		c.Crash = &crash
	}
	c.StackFree = maps.Clone(d.StackFree)
	return &c
}

func (d *deviceDiag) carryFrom(prev *deviceDiag) {
	if prev == nil {
		return
	}
	d.Reboots, d.PrevResetReason, d.RebootsSinceSeen = prev.Reboots, prev.PrevResetReason, prev.RebootsSinceSeen
	if prev.Boots == 0 || d.Boots == 0 || d.Boots == prev.Boots {
		return
	}
	jump := int64(1)
	if d.Boots > prev.Boots {
		jump = d.Boots - prev.Boots
	}
	d.Reboots += jump
	d.PrevResetReason, d.RebootsSinceSeen = prev.ResetReason, 0
	if jump > 1 {
		d.PrevResetReason, d.RebootsSinceSeen = "", jump
	}
}

func newDiagCrash(prev, cur *deviceDiag) *deviceCrash {
	if cur == nil || cur.Crash == nil {
		return nil
	}
	if prev != nil && prev.Crash != nil && *prev.Crash == *cur.Crash {
		return nil
	}
	c := *cur.Crash
	return &c
}
