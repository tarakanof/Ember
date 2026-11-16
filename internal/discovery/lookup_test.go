package discovery

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/brutella/dnssd"
)

func TestEndCanceledReportsCanceledWhenParentDeadlinePasses(t *testing.T) {
	var seen error
	lookup := endCanceled(func(ctx context.Context, _ string, _ dnssd.AddFunc, _ dnssd.RmvFunc) error {
		<-ctx.Done()
		seen = ctx.Err()
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_ = lookup(ctx, "_x._tcp.local.", func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
	if !errors.Is(seen, context.Canceled) {
		t.Fatalf("lookup ctx ended with %v, want context.Canceled", seen)
	}
}

func TestEndCanceledCancelsLookupContextOnReturn(t *testing.T) {
	var inner context.Context
	lookup := endCanceled(func(ctx context.Context, _ string, _ dnssd.AddFunc, _ dnssd.RmvFunc) error {
		inner = ctx
		return nil
	})
	_ = lookup(context.Background(), "_x._tcp.local.", func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
	if !errors.Is(inner.Err(), context.Canceled) {
		t.Fatalf("lookup ctx after return: %v, want context.Canceled", inner.Err())
	}
}

func processCPU() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func TestLookupTypeLeavesNoSpinningReadersAfterTimeout(t *testing.T) {
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		err := lookupType(ctx, "_ember-leak-test._tcp.local.", func(dnssd.BrowseEntry) {}, func(dnssd.BrowseEntry) {})
		cancel()
		if err != nil && time.Since(start) < 50*time.Millisecond {
			t.Skipf("no multicast socket here: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	before := processCPU()
	time.Sleep(time.Second)
	if burn := processCPU() - before; burn > 300*time.Millisecond {
		t.Fatalf("idle process burned %v CPU in 1s after timed-out lookups", burn)
	}
}
