package main

import (
	"bytes"
	"context"
)

func dispatchHook(ctx context.Context, event string, stdin []byte, cfg Config) {
	dispatchHookFrom(ctx, event, bytes.NewReader(stdin), cfg)
}
