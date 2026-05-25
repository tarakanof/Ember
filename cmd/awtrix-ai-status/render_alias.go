package main

import "github.com/dt/awtrix-ai-status/internal/render"

// Type aliases keep the rest of package main source-compatible after the
// rendering core (and the Session/Snapshot/Render types) moved to
// internal/render. These are aliases, not new types, so existing field
// access, construction, and JSON marshaling are unchanged.
type (
	Session  = render.Session
	Snapshot = render.Snapshot
	Render   = render.Render
)
