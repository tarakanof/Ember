package main

import (
	"bytes"
	"context"
)

// dispatchHook runs one hook from an in-memory stdin.
func dispatchHook(ctx context.Context, event string, stdin []byte, cfg Config) {
	dispatchHookFrom(ctx, event, bytes.NewReader(stdin), cfg)
}
