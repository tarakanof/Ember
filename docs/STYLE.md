# Code Style & Implementation Guide

This document is the canonical style guide for this repository. AI assistants (Claude Code, Codex CLI, Gemini CLI) and humans alike are expected to follow it. `AGENTS.md` carries an overview; this file is the deep dive.

The guide is split in two halves:

- **Part I — Universal Principles.** Language-agnostic rules that govern how we design, test, and ship code anywhere in this repo (Go server, future Swift/Go menu bar app, Bash hooks).
- **Part II — Go (1.22+) Specifics.** Concrete rules for the Go server and any future Go code.

Two short appendices follow: commit/PR conventions and a list of named anti-patterns.

---

## Part I — Universal Principles

### 1. Files focused, boundaries clear

A unit (file, package, module) does one thing well. You should be able to answer "what does this do, how do you use it, what does it depend on" without reading internals. If a file grows past the point where you can hold its entire surface area in your head — usually somewhere between 800 and 1200 lines for Go, less for prose — it's doing too much. Split by responsibility, not by technical layer (i.e., `store.go` + `server.go`, **not** `models/` + `handlers/`).

Files that change together live together. A handler and the type it handles belong in the same file unless either has grown enough to deserve its own.

### 2. Boring is a feature

Pick the most boring solution that fits the requirement. Boring code is faster to write, faster to read a year later, and faster to debug at 2 a.m.

> *"This service is intentionally light: one Go binary, no runtime dependencies, no Go package dependencies beyond the standard library."* — `README.md`

That stance is the project's default. We deviate from it only when a dependency clearly earns its cost (see §11).

### 3. YAGNI ruthlessly

Don't add features, refactors, or abstractions beyond what the task requires. A bug fix doesn't need surrounding cleanup; a one-shot operation doesn't need a helper. Don't design for hypothetical future requirements. **Three similar lines is better than a premature abstraction.**

No half-finished implementations. If the work doesn't merit being done now, leave a single sharp note in the relevant spec or task — never a `TODO:` floating in a source file.

### 4. Errors are values, never silent

- Every error gets one of: handled, wrapped with context and returned, or — at the program boundary — logged once and surfaced to the user.
- **Never both log and return the same error.** Pick one. If you log it, it's handled; if you return it, it's the caller's problem.
- Wrap with context the first time the error crosses a meaningful boundary: `fmt.Errorf("read config: %w", err)`. Each wrapper adds *new* information, not a restatement of the underlying error.
- Use `errors.Is` and `errors.As` for matching. Sentinel errors are package-level `var ErrFoo = errors.New(...)`.
- Don't introduce error types just to carry a string. Plain `errors.New` is fine until you need programmatic matching.

### 5. State is explicit, ownership is clear

- No package-level mutable state. Pass dependencies (logger, config, store) explicitly through constructors.
- A goroutine's lifetime is owned by something. That owner is responsible for shutdown (via `context.Context` cancellation or an explicit channel).
- Mutexes guard *exactly one logical thing*. Document what (`mu sync.Mutex // protects sessions`).
- Prefer "share by communicating": channels for ownership transfer between goroutines; mutexes for in-place state protection. Don't mix metaphors on the same datum.

### 6. Tests verify behavior, not internals

- TDD by default for new behavior. Write the failing test first, watch it fail with the right error, then make it pass.
- Test through the smallest interface that exercises the behavior. For an HTTP server, that's `httptest.NewServer(app.routes())` and a real HTTP client — not a direct call to a handler.
- Don't mock what you control. Use a small in-process fake or the real implementation. Mocks are for things you don't own (third-party APIs, the network, the clock).
- Each test names exactly one behavior in its function name. `TestPostStatusRejectsEmptyTool` beats `TestStatusValidation`.
- Use `t.Helper()` in test helpers so failures point at the call site, not the helper.
- Use `t.Cleanup` for teardown; never rely on `defer` in test setup-helper code that's called multiple times.

### 7. Observability is a first-class output

Every non-trivial service emits structured logs at decision points. The signal-to-noise ratio matters more than the volume.

- Log on transitions (request received, request rejected, session reaped, config reloaded). Don't log on success of every routine operation.
- Fields must be machine-greppable: `source=dt-mbp tool=claude session=awtrix state=running`. No prose strings as keys.
- Errors logged at `WARN` are recoverable; `ERROR` is "we couldn't do the thing the user asked for"; `FATAL` is "we are about to exit".
- One field name per concept across the codebase. Don't mix `session_id`, `sess`, `s` in the same project.

### 8. Configuration: load → validate → use

Configuration loading happens once, at startup. After that, the rest of the program sees a validated `Config` value (or types derived from it). Never reach for `os.Getenv` from inside business logic.

- Required fields are non-empty strings or non-zero numbers; the loader rejects empty values with a clear message.
- Secrets come from env vars only. Never hard-coded, never committed, never logged.
- Defaults are explicit in code (`defaultConfig()`), not implicit ("if zero, treat as 30s"). Zero is a value, not "missing".

### 9. Comments explain *why*, never *what*

Default to writing zero comments. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, behavior that would surprise a reader.

- Don't explain what the code does — well-named identifiers already do that.
- Don't reference the current task, fix, or callers ("used by X", "added for the Y flow"). That belongs in the PR description and rots as the codebase evolves.
- One short line max. Multi-paragraph docstrings and multi-line comment blocks are almost never necessary.
- Exception: package documentation (`// Package foo does X`) and exported API godoc comments. Those follow Go's convention of "complete sentence starting with the identifier name".

### 10. Concurrency is explicit and bounded

- Every goroutine has an owner that knows when it stops. Goroutines that "run forever" need a `<-ctx.Done()` exit path.
- Buffered channels: size 0 (unbuffered) or 1 by default. Other sizes need a sentence of justification ("queue absorbs N bursts before backpressure").
- `sync.RWMutex` only when read contention is measurable. Default is `sync.Mutex`.
- Pass `context.Context` as the first parameter to every function that does I/O or might block. Never store it in a struct.

### 11. Dependencies have to earn their place

Stdlib first. Take a third-party dependency when it clearly does more than the stdlib does (and well), AND when reimplementing it ourselves would be a known-bad component.

- Two examples from this repo:
  - **MQTT client** — we tried hand-rolling MQTT 3.1.1 because it looked small. The result had no keepalive, no reconnect, no QoS. We deleted it. If MQTT comes back, it'll be `eclipse/paho.mqtt.golang`.
  - **HTTP server** — net/http with 1.22+ pattern matching is enough for everything this project needs. We don't need chi/echo/gin.
- A new dependency requires a one-line justification in the commit body.
- Pin versions; don't trust `latest`. Run `go mod tidy` after every dep change.

### 12. Documentation has a layered structure

- `README.md` — how to run it, basic config, endpoints. Skimmable in 60 seconds.
- `AGENTS.md` — repository conventions, build/test commands, secrets, runtime notes. The first thing any AI assistant reads.
- `CLAUDE.md` — points at AGENTS.md (we keep one source of truth).
- `docs/STYLE.md` — this file.
- `docs/superpowers/specs/` — design contracts for non-trivial work, dated.
- `docs/superpowers/plans/` — implementation plans matching specs, dated.
- Inline docs (godoc) — exported API only.

When in doubt, write the spec, then point at it from the code's commit message. Don't paste the spec into the code.

---

## Part II — Go (1.22+) Specifics

### Project layout

- `cmd/<binary>/main.go` for entrypoints.
- `internal/` for code that should not be importable from outside this module.
- **No `pkg/`** — Go has been moving away from this convention; it adds a directory with no benefit.
- Test files live next to the code they test as `_test.go`. Black-box test packages (`package foo_test`) are fine for testing the public API; same-package tests are fine for internals.

### Files in this repo

- `cmd/awtrix-ai-status/main.go` is the single-file regime. Acceptable until ~1200 lines, after which split by responsibility:
  - `server.go` — HTTP handlers, routing, middleware
  - `store.go` — `App`, sessions, render priority
  - `awtrix.go` — `HTTPPublisher`
  - `config.go` — `Config`, `defaultConfig`, `loadConfig`, validation

### HTTP servers

- Use net/http's pattern-matching ServeMux (Go 1.22+): `mux.HandleFunc("POST /v1/status", h)`. Don't reach for chi/echo/gin unless the stdlib genuinely can't express what's needed.
- Always set `ReadHeaderTimeout` on `http.Server`.
- Compose middleware as `http.Handler` decorators. No middleware framework needed.
- Group endpoints by auth surface: read endpoints unauthenticated, write endpoints behind a single middleware.
- One handler function per route. Keep handlers thin: parse → validate → call domain method → render response. Domain logic does not live in handlers.

### Error handling

- Wrap with `fmt.Errorf("describe what failed: %w", err)`. The first verb describes the operation that failed, not the error.
- Sentinel errors at package level: `var ErrSessionNotFound = errors.New("session not found")`.
- Don't define custom error types unless callers need to extract structured data. A plain string error is enough for most cases.
- HTTP handlers translate domain errors to status codes at the boundary, not before. The store returns `ErrSessionNotFound`; the handler returns 404.
- `defer resp.Body.Close()` immediately after every successful HTTP request — never later.

### Logging

- `log/slog` is the standard. Use `slog.New(slog.NewTextHandler(...))` for human-readable, JSON handler for production aggregation.
- Pass a `*slog.Logger` via dependency injection. Don't log to a global.
- Structured fields only: `logger.Info("session reaped", "source", s.Source, "tool", s.Tool)`. Never embed values in the message string.
- Levels: `Debug` for verbose dev-only signal; `Info` for state transitions and request decisions; `Warn` for recoverable problems; `Error` for failures the user will notice; no `Fatal` (use `os.Exit(1)` after a clear log).

### Testing

- Table-driven tests for any function tested with multiple inputs:
  ```go
  cases := []struct {
      name string
      in   StatusRequest
      want int
  }{
      {"empty source", StatusRequest{Tool: "x", Session: "y"}, http.StatusBadRequest},
  }
  for _, c := range cases {
      t.Run(c.name, func(t *testing.T) { ... })
  }
  ```
- Subtests with `t.Run(name, func(t *testing.T))` for any test that runs more than one case.
- `t.Helper()` in every test helper.
- `t.Cleanup(fn)` for teardown.
- `httptest.NewServer(app.routes())` for HTTP integration; real `http.DefaultClient` requests against it.
- No `time.Sleep` in tests. If a test needs to assert on timing, inject a `time.Now` function.
- No `testify`/`assert` until and unless we have ~50 tests and the boilerplate cost is real. Plain `t.Fatal` / `t.Errorf` is enough today.
- Run `go test ./... -race` before merging anything that touches goroutines.

### Concurrency

- Every long-running goroutine takes a `context.Context` and exits on `<-ctx.Done()`.
- Mutexes are unexported and named for what they guard:
  ```go
  type App struct {
      mu       sync.Mutex // protects sessions
      sessions map[string]Session
  }
  ```
- For "wait for N goroutines, fail together": `golang.org/x/sync/errgroup`. Worth the dependency.
- For "do once": `sync.Once`. Don't roll your own with channels.
- Channels are for handoff and signaling, not for storing state. State lives in a struct guarded by a mutex.

### Generics

- Use sparingly. Most code is fine with concrete types or interfaces.
- Good fits: container types (`Set[T]`, `Cache[K, V]`), generic helpers (`Map`, `Filter`, `MustEnv`).
- Bad fits: business logic, "make it work for any type" interfaces, anything that ends up with `any` constraints.

### Naming

- Receivers: short (1–3 letters), consistent across all methods of a type. `(a *App)`, `(p *Publisher)`, `(c Config)` (value receiver if no mutation).
- Exported names: `MixedCaps`. Unexported: `mixedCaps`.
- No package name in the type name: `auth.Token`, not `auth.AuthToken`.
- Interface names ending in `-er` only when the interface has exactly one method (`Publisher`, `Renderer`).
- Test functions: `TestX`, `Test_X` is allowed for grouped variants; subtests use lowercase descriptive names.

### Imports and formatting

- `gofmt` is non-negotiable. It runs in CI and pre-commit.
- Import groups, separated by blank lines:
  1. stdlib
  2. third-party
  3. this module
- `goimports` handles this automatically; we run it.

### Configuration patterns

- One `Config` struct per binary. Sub-structs for cohesive groups (`HTTPConfig`, `AuthConfig`).
- `defaultConfig()` returns the defaults; `loadConfig(path)` reads JSON and overlays; `(c *Config) applyDefaults()` fills zero values; validation rejects required-but-empty.
- Env vars are explicitly bridged at config-load time, not scattered through the codebase. `os.Getenv("STATUS_TOKEN")` lives in `applyDefaults` only.
- For an env-name override pattern: `Auth.StatusTokenEnv = "STATUS_TOKEN"` with `os.Getenv(c.Auth.StatusTokenEnv)`. This makes the env name swappable without forking config logic.

### Observability patterns specific to this project

- Every state change in the session store gets a `slog.Info` at the boundary that caused it: HTTP handler logs the request, the reaper logs the eviction, the publisher logs publish failures.
- AWTRIX publish failures are `Warn`, not `Error` — the device may be temporarily unreachable; the next refresh will retry.
- No per-heartbeat log line. Heartbeats are the steady-state.

### HTTP-server idioms

- `http.NewServeMux()` for the public mux; nest a private mux under a path prefix for auth surfaces:
  ```go
  writeMux := http.NewServeMux()
  writeMux.HandleFunc("POST /v1/status", a.handleStatus)
  mux.Handle("/v1/", requireAuth(token, writeMux))
  ```
- `decodeJSON` once, top of every handler, with a body-size limit:
  ```go
  dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
  dec.DisallowUnknownFields()
  ```
- Always `defer r.Body.Close()` — even though Go usually does it for you, it's free insurance.
- Status codes:
  - 200 for "I did the thing and here's the result"
  - 204 for "I did the thing and there's no body"
  - 400 for malformed/invalid input
  - 401 for missing/wrong auth
  - 404 for not-found resources
  - 502 for upstream failures (e.g., AWTRIX device unreachable)
  - Don't use 403 unless we have a real authorization-vs-authentication distinction.

### Bash and shell

When writing producer hooks (sub-project B/C):

- `set -euo pipefail` at the top of every script.
- `IFS=$'\n\t'` if the script processes newline-separated input.
- Quote every variable expansion: `"$var"`, never `$var`.
- Test scripts with `shellcheck` before merging.
- Read tokens from env, not args. Args show up in `ps`.

---

## Appendix A — Commit & PR conventions

- Subject ≤ 70 characters, imperative mood, no trailing period: `Add bearer-token auth on write endpoints`.
- Body wraps at 72 characters and explains *why*, not *what* (the diff already shows what).
- One logical change per commit. If you need "and" in the subject, you have two commits.
- Reference the relevant spec/plan file path in the commit body when applicable.
- Co-Authored-By trailer for AI-assisted commits (Claude / Codex / Gemini).
- Never amend a pushed commit.
- Never `--no-verify`. If a hook fails, fix the underlying issue.

---

## Appendix B — Anti-patterns to avoid (named, with antidotes)

| Anti-pattern | Antidote |
|---|---|
| Logging *and* returning the same error | Pick one — log at the boundary or return for the caller to handle. |
| `// TODO: handle errors` left in code | Either handle the error now or delete the line. The compiler enforces handling. |
| "Add error handling" in a plan/spec without specifying what | Specify the exact failure mode and the exact response. |
| Premature abstraction (extracting a helper called once) | Inline it. Wait until 3+ callers exist. |
| Backwards-compat shims for code with zero current consumers | Delete the old code; we have git. |
| Mocking what we control | Use the real implementation in tests. Mocks are for the network, the clock, and third-party APIs. |
| Global mutable state (`var globalLogger *slog.Logger`) | Inject it. Constructors accept what their type needs. |
| `init()` functions doing non-trivial setup | Move setup into `main()` or an explicit `Setup()` call. `init` is for compile-time constants. |
| `interface{}` / `any` in business types | Use a concrete type or a small purpose-built interface. `any` is for JSON edges and reflection only. |
| `time.Sleep` in tests | Inject a clock or use channels to synchronize. |
| `panic()` for non-programmer errors | Return an error. Panics are for "the program is in an impossible state". |
| Naked boolean parameters (`do(true)`) | Use a typed enum or a struct with a named field: `do(Mode{Strict: true})`. |
| `for i := 0; i < len(s); i++` over a slice | `for i, v := range s`. The range form is a contract — it doesn't lie about iteration. |
| Tests named `TestX` that test five things | One behavior per test name. Split or use subtests. |
| `if err != nil { return err }` with no wrap | Wrap with context: `return fmt.Errorf("read config: %w", err)`. |

---

## Appendix C — How AI assistants should use this guide

Claude Code, Codex CLI, and Gemini CLI: when working in this repository, you are expected to:

1. **Read this file before any non-trivial code change.** AGENTS.md will direct you here.
2. **When in doubt, prefer the most boring option that fits.**
3. **If a rule conflicts with the user's explicit instruction, the user wins** — but flag the conflict before silently complying.
4. **If you can't follow a rule because of a constraint** (e.g., the task explicitly requires a third-party dep), say so in the PR description, not silently.
5. **Don't vendor this guide elsewhere.** If you're working in a different repo, check that repo's STYLE.md (or absence thereof).

The guide evolves. If you find yourself repeatedly reaching for an exception, propose an edit instead of slowly drifting.
