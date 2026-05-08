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

No half-finished implementations. If the work doesn't merit being done now, leave a single sharp note in the relevant spec or task. Floating `TODO:` markers in source code are not acceptable; anchored ones (with an issue number, spec path, or deadline) are — see §9.

### 4. Errors are values, never silent

- Every error gets one of: handled, wrapped with context and returned, or — at the program boundary — logged once and surfaced to the user.
- **Never both log and return the same error.** Pick one. If you log it, it's handled; if you return it, it's the caller's problem.
- Wrap with context the first time the error crosses a meaningful boundary: `fmt.Errorf("read config: %w", err)`. Each wrapper adds *new* information, not a restatement of the underlying error.
- Use `errors.Is` and `errors.As` for matching. Sentinel errors are package-level `var ErrFoo = errors.New(...)` — the one accepted exception to "no package-level mutable state" (§5), because they're conventional and effectively immutable.
- Use `errors.Join(a, b)` when multiple independent errors must be surfaced together (e.g., a deferred close failure plus the original error).
- Treat `context.Canceled` and `context.DeadlineExceeded` as expected control-flow signals, not failures. Handlers translate them to a non-error response when shutting down; reapers and tickers exit silently.
- Intentional ignored errors get the explicit `_ = thing.Close()` form, never a bare `thing.Close()`. The reader can tell intent from syntax.
- In `defer`, capture close/flush errors when they could mask data loss: `defer func() { err = errors.Join(err, w.Close()) }()` with a named return.
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

- Log on transitions and decisions: request rejected, session reaped, config reloaded, publish failed. Don't log on success of every routine operation, and don't log heartbeats at `Info`.
- Fields must be machine-greppable: `source=dt-mbp tool=claude session=awtrix state=running`. No prose strings as keys.
- Field key naming is `snake_case` for slog attributes, consistently across the codebase. One field name per concept — don't mix `session_id`, `sess`, `s`.
- Levels: `Debug` for high-volume signal useful only when investigating (heartbeats, every successful publish); `Info` for state transitions and meaningful decisions; `Warn` for recoverable problems (transient publish failure, reaper evicted a stale session); `Error` for failures the user will notice. **No `Fatal` level** — log at `Error` and `os.Exit(1)`, in that order, only from `main()`.

### 8. Configuration: load → validate → use

Configuration loading happens once, at startup. After that, the rest of the program sees a validated `Config` value (or types derived from it). Never reach for `os.Getenv` from inside business logic.

- Required fields are non-empty strings or non-zero numbers; the loader rejects empty values with a clear message.
- For *optional* fields, `defaultConfig()` returns a fully-populated value and `applyDefaults()` fills any zero-valued field with that default. The contract: zero on an optional field means "use the default"; zero on a required field is a config error.
- Secrets come from env vars only. Never hard-coded, never committed, never logged.
- Use `slog.LogValuer` (or equivalent redaction) on any type carrying secrets so a stray `slog.Info("config loaded", "cfg", cfg)` can't leak them.

### 9. Comments explain *why*, never *what*

Two distinct cases:

**Inline comments** (inside function bodies): default to zero. Only add one when the WHY is non-obvious — a hidden constraint, a subtle invariant, a workaround for a specific bug, behavior that would surprise a reader. One short line max. Don't restate what the code does, and don't reference the current task or PR (that belongs in the commit message).

**Godoc on exported APIs**: required, not optional. Every exported type, function, method, and variable gets a doc comment that starts with the identifier name and forms a complete sentence. Doc the *contract* (preconditions, postconditions, error cases), not the implementation. A package gets a `// Package foo …` comment in one file.

`TODO` markers are acceptable when they include a concrete reference (issue number, spec path, or a sentence with a deadline): `// TODO: drop after sub-project E ships`. Floating `// TODO: handle this` with no anchor is not — that's how dead code accumulates.

### 10. Concurrency is explicit and bounded

- Every goroutine has an owner that knows when it stops. Goroutines that "run forever" need a `<-ctx.Done()` exit path.
- Buffered channels: size 0 (unbuffered) or 1 by default. Other sizes need a sentence of justification ("queue absorbs N bursts before backpressure").
- `sync.RWMutex` only when read contention is measurable. Default is `sync.Mutex`.
- Pass `context.Context` as the first parameter to every function that does I/O or might block. Don't store it in a struct **unless the struct's lifetime is request-scoped** (this is the `*http.Request`/`httptrace` exception, not a license to put `ctx` on long-lived application types).
- `context.AfterFunc` (Go 1.21+) is the right tool for "run this cleanup if the context is cancelled" — cleaner than spawning a watchdog goroutine.

### 11. Dependencies have to earn their place

**Stdlib by default.** Take a third-party dependency when it clearly does more than the stdlib does (and well), AND when a reasonable in-house reimplementation would be a known-bad component. The rule is "every dep must earn its slot", not "no deps ever".

- Two real examples from this repo:
  - **MQTT client** — we tried hand-rolling MQTT 3.1.1. The result had no keepalive, no reconnect, no QoS. We deleted it. If MQTT comes back, it'll be `eclipse/paho.mqtt.golang` — taking the dep is the right call there.
  - **HTTP routing** — net/http with 1.22+ pattern matching is enough for everything this project needs. No chi/echo/gin justified.
- Examples of deps that *would* earn their slot: `golang.org/x/sync/errgroup` for fail-together goroutine groups, `paho.mqtt.golang` for MQTT, `prometheus/client_golang` if metrics ever become real. We don't ship them today, but we will if the need is.
- A new dependency requires a one-line justification in the commit body.
- Pin versions; don't trust `latest`. Run `go mod tidy` after every dep change. Run `govulncheck ./...` in CI; an unfixed advisory blocks merge.

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
- This project does not use `pkg/`; it's a contested convention and we don't need the extra directory layer. Other projects may legitimately differ.
- Test files live next to the code they test as `_test.go`. Black-box test packages (`package foo_test`) are fine for testing the public API; same-package tests are fine for internals.

### Files in this repo

- `cmd/awtrix-ai-status/main.go` is the single-file regime. Acceptable until ~1200 lines, after which split by responsibility:
  - `server.go` — HTTP handlers, routing, middleware
  - `store.go` — `App`, sessions, render priority
  - `awtrix.go` — `HTTPPublisher`
  - `config.go` — `Config`, `defaultConfig`, `loadConfig`, validation

### HTTP servers

- Use net/http's pattern-matching ServeMux (Go 1.22+): `mux.HandleFunc("POST /v1/status", h)`. Don't reach for chi/echo/gin unless the stdlib genuinely can't express what's needed.
- **Set every timeout** on `http.Server`, not just `ReadHeaderTimeout`. A defensible baseline:
  ```go
  &http.Server{
      Addr:              cfg.HTTP.Addr,
      Handler:           handler,
      ReadHeaderTimeout: 5 * time.Second,
      ReadTimeout:       30 * time.Second,
      WriteTimeout:      30 * time.Second,
      IdleTimeout:       120 * time.Second,
  }
  ```
  Outbound HTTP clients always set `Timeout`; never use `http.DefaultClient` for production calls.
- **Graceful shutdown** is mandatory: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` for the cancel signal, then `srv.Shutdown(shutdownCtx)` with a bounded timeout. Background workers (publishers, reapers) drain via the same context.
- Compose middleware as `http.Handler` decorators. No middleware framework needed.
- Group endpoints by auth surface: read endpoints unauthenticated, write endpoints behind a single middleware.
- Default to one handler per route. Sharing a handler across closely related routes (e.g., a typed handler struct with `(h *statusHandler) ServeHTTP`) is fine when the behavior really is the same. Don't force-split for orthogonality.
- Keep handlers thin: parse → validate → call domain method → render response. Domain logic does not live in handlers.

### Error handling

- Wrap with `fmt.Errorf("describe what failed: %w", err)`. The first verb describes the operation that failed, not the error.
- Sentinel errors at package level: `var ErrSessionNotFound = errors.New("session not found")`.
- Don't define custom error types unless callers need to extract structured data. A plain string error is enough for most cases.
- HTTP handlers translate domain errors to status codes at the boundary, not before. The store returns `ErrSessionNotFound`; the handler returns 404.
- For multiple independent failures, use `errors.Join(err1, err2)` — common case is a deferred close error joined with the operation's error.
- `context.Canceled` and `context.DeadlineExceeded` are not failures. Don't log them at `Error`; either return them as-is or treat as "shutting down" depending on context.
- **Client-side HTTP**: `defer resp.Body.Close()` is mandatory — leaks connections otherwise.
- **Server-side HTTP**: net/http closes the request body for you when the handler returns. Adding `defer r.Body.Close()` is harmless but unnecessary; don't lean on it as if it were doing real work.

### Logging

- `log/slog` is the standard. Use `slog.New(slog.NewTextHandler(...))` for human-readable, JSON handler for production aggregation.
- Pass a `*slog.Logger` via dependency injection. Don't log to a global.
- Structured fields only: `logger.Info("session reaped", "source", s.Source, "tool", s.Tool)`. Never embed values in the message string.
- Use the `Context`-aware variants (`logger.InfoContext(ctx, ...)`, `ErrorContext`, etc.) anywhere you have a `ctx`. They propagate trace/request IDs through `slog.Handler` implementations that respect them, and cost nothing if no handler cares.
- Field key naming is `snake_case` consistently across the codebase (matches the JSON conventions on the wire).
- Make level mutable at runtime via `slog.LevelVar`. Wire it to a config field or a SIGUSR1 handler so we can crank to `Debug` in production without redeploying.
- Implement `slog.LogValuer` on any type that contains a secret, so a stray `logger.Info("auth", "cfg", auth)` redacts automatically.
- Levels: `Debug` for high-volume / per-request / per-heartbeat signal; `Info` for state transitions and decisions; `Warn` for recoverable problems; `Error` for failures the user will notice. No `Fatal` — log at `Error` and `os.Exit(1)` from `main()`.

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
- `t.TempDir()` for filesystem fixtures, `t.Setenv()` for env-var fixtures, `t.Chdir()` (Go 1.24+) for working-directory fixtures, `t.Context()` (Go 1.24+) for request-scoped contexts. All auto-clean up.
- `t.Parallel()` whenever a test is genuinely independent of others. Combine with `t.Setenv()` carefully — `t.Setenv` requires the test be non-parallel within its package's parallel set.
- HTTP integration: `srv := httptest.NewServer(app.routes())` then `srv.Client()` for the request client. Don't use `http.DefaultClient` in tests — it bakes in global behavior and shares connection pool with whatever else.
- No `time.Sleep` in tests. For coordinating goroutines, prefer Go 1.25's `testing/synctest` (gives synthetic time and lets you advance the clock deterministically) or a clock interface injected via the `App`'s constructor.
- Plain `t.Fatal` / `t.Errorf` is the default. `testify/require` is acceptable when assertion-heavy tests genuinely improve readability — pick the threshold by feel rather than count, and apply consistently within a package.
- Fuzz parsers and any other code accepting unstructured input: `func FuzzX(f *testing.F)`.
- Golden files for tests that compare against large expected output: regenerate via `go test -update`, never edit by hand.
- Run `go test ./... -race` before merging anything that touches goroutines. CI runs `-race` on every test.

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
- Good fits: container types (`Set[T]`, `Cache[K, V]`), small generic helpers where the concrete alternative is obviously worse (`MustEnv[T]`, `cmp.Or[T]` already in stdlib).
- Plain loops are usually clearer than `Map`/`Filter`/`Reduce` in Go. Reach for them when the loop is harder to read than the higher-order function — not as a default style.
- Bad fits: business logic, "make it work for any type" interfaces, anything that ends up with `any` constraints.

### Stdlib utility packages worth knowing

- `slices` — sort, search, equal, contains, sorted-insert, etc. Use this instead of writing your own.
- `maps` — clone, equal, keys (`maps.Keys` returns an iterator in 1.23+), copy.
- `cmp` — `cmp.Or`, `cmp.Compare`, `cmp.Less`. Tiny but high-leverage.
- `iter` — single-use iterators (Go 1.23+). The `range` form over functions returning iterators is the right modern shape for "stream of values you want to iterate once".

Pre-allocate when the size is known: `make([]T, 0, n)`, `make(map[K]V, n)`. The cost in clarity is one number; the cost in allocations is real.

Prefer types whose zero value is usable (`var mu sync.Mutex` works, no `NewMutex()` needed). Design your own types the same way: a `*App{}` from `&App{}` should not panic on first method call.

### Naming

- Receivers: short (1–3 letters), consistent across all methods of a type. `(a *App)`, `(p *Publisher)`, `(c Config)`. Receiver choice (pointer vs value) depends on size, mutation, embedded locks, and method-set consistency — when in doubt, all methods on a type share the same receiver style. Don't mix.
- Exported names: `MixedCaps`. Unexported: `mixedCaps`.
- No package name in the type name: `auth.Token`, not `auth.AuthToken`.
- Single-method interfaces conventionally end in `-er` (`Publisher`, `Renderer`, `io.Reader`). Multi-method interfaces use plain descriptive names (`http.Handler`, `sort.Interface`). Don't force-`-er` on multi-method interfaces.
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

- State *transitions* in the session store get a `slog.InfoContext` at the boundary that caused them: handler logs a rejection, store logs an eviction, publisher logs a failed publish. Successful publishes and heartbeat upserts log at `Debug` only — they're the steady state.
- AWTRIX publish failures are `Warn`, not `Error` — the device may be temporarily unreachable; the next refresh will retry. Repeated `Warn` for the same failure mode is fine; we don't suppress.
- Request decisions (auth fail, validation fail, 4xx) log at `Info`. The successful 2xx path stays at `Debug` — visible in dev, quiet in prod.

### HTTP-server idioms

- `http.NewServeMux()` for the public mux; nest a private mux under a path prefix for auth surfaces:
  ```go
  writeMux := http.NewServeMux()
  writeMux.HandleFunc("POST /v1/status", a.handleStatus)
  mux.Handle("/v1/", requireAuth(token, writeMux))
  ```
- Body parsing uses `http.MaxBytesReader` (not `io.LimitReader`) so oversized requests get a clean 413 instead of a truncation:
  ```go
  r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
  dec := json.NewDecoder(r.Body)
  dec.DisallowUnknownFields()
  if err := dec.Decode(&v); err != nil { /* 400 or 413 */ }
  if dec.More() { /* trailing tokens — return 400 */ }
  ```
- The server runtime closes the request body for you when the handler returns; no need to `defer r.Body.Close()` on the server side. (Client side is different — it's mandatory there to free the connection.)
- Status codes:
  - 200 for "I did the thing and here's the result"
  - 204 for "I did the thing and there's no body"
  - 400 for malformed/invalid input (including unknown fields and trailing tokens)
  - 401 for missing/wrong auth
  - 404 for not-found resources
  - 413 for body-too-large (`http.MaxBytesReader` triggers this for us)
  - 502 for upstream failures (e.g., AWTRIX device unreachable)
  - Don't use 403 unless we have a real authorization-vs-authentication distinction.

### Bash and shell

When writing producer hooks (sub-project B/C):

- `set -euo pipefail` at the top of every script.
- `IFS=$'\n\t'` if the script processes newline-separated input.
- Quote every variable expansion: `"$var"`, never `$var`.
- Test scripts with `shellcheck` before merging.
- Read tokens from env, not args. Args show up in `ps`.

### Tooling

- `go vet ./...` — runs in CI, must pass clean.
- `staticcheck ./...` — additional analyzers; fix or annotate.
- `govulncheck ./...` — vulnerability scan, blocks merge on unfixed advisories.
- `go test ./... -race -shuffle=on` in CI; `-race` everywhere, `-shuffle=on` to catch order dependencies.
- `go test -json` is what CI consumes (better failure parsing than text).
- Pin developer tools (e.g., `staticcheck`, `gofumpt`) using Go 1.24's `tool` directives in `go.mod`. Avoids "works on my machine".

---

## Appendix A — Commit & PR conventions

- Subject ≤ 70 characters, imperative mood, no trailing period: `Add bearer-token auth on write endpoints`.
- Body wraps at 72 characters and explains *why*, not *what* (the diff already shows what).
- One logical change per commit. If you need "and" in the subject, you have two commits.
- Reference the relevant spec/plan file path in the commit body when applicable, even though those files are not tracked (see below) — the references serve as local-context pointers for the author.
- **No `Co-Authored-By` trailers** for AI-assisted commits. Author attribution stays with the human pushing the commit.
- Never amend a pushed commit.
- Never `--no-verify`. If a hook fails, fix the underlying issue.

### Superpowers artifacts are not tracked

Files under `docs/superpowers/` (specs, plans, brainstorm notes produced by superpowers-skill workflows) are gitignored. They live on the contributor's local disk and inform the work, but the repo itself ships only the implementation, `docs/STYLE.md`, `README.md`, and `AGENTS.md`.

---

## Appendix B — Anti-patterns to avoid (named, with antidotes)

| Anti-pattern | Antidote |
|---|---|
| Logging *and* returning the same error | Pick one — log at the boundary or return for the caller to handle. |
| Floating `// TODO:` with no anchor | Either anchor it (issue number, spec path, deadline) or delete it. Anchored TODOs are fine. |
| "Add error handling" in a plan/spec without specifying what | Specify the exact failure mode and the exact response. |
| Premature abstraction (extracting a helper called once) | Inline it. Wait until 3+ callers exist. |
| Backwards-compat shims for code with zero current consumers | Delete the old code; we have git. |
| Mocking what we control | Use the real implementation in tests. Mocks are for the network, the clock, and third-party APIs. |
| Global mutable state (`var globalLogger *slog.Logger`) | Inject it. Constructors accept what their type needs. |
| `init()` functions doing non-trivial setup | Move setup into `main()` or an explicit `Setup()` call. Reserve `init` for things that genuinely cannot be deferred (e.g., registering with a global the language requires). |
| `interface{}` / `any` in business types | Use a concrete type or a small purpose-built interface. `any` is for JSON edges, reflection, and generic constraints only. |
| `time.Sleep` in tests | Use `testing/synctest` or inject a clock. |
| `panic()` for non-programmer errors | Return an error. Panics are for "the program is in an impossible state". |
| Naked boolean parameters (`do(true)`) | Use a typed enum or a struct with a named field: `do(Mode{Strict: true})`. |
| Tests named `TestX` that test five things | One behavior per test name. Split or use subtests. |
| `if err != nil { return err }` with no wrap | Wrap with context: `return fmt.Errorf("read config: %w", err)`. |
| `http.DefaultClient` in tests or production code | Use `srv.Client()` in tests; an explicit `&http.Client{Timeout: …}` in production. |
| Cargo-cult `defer r.Body.Close()` in HTTP handlers | The server runtime closes it. Save the line for client-side calls where it actually matters. |
| Logging `context.Canceled` at `Error` | It's a shutdown signal, not a failure. Return it or log at `Debug`. |

---

## Appendix C — How AI assistants should use this guide

Claude Code, Codex CLI, and Gemini CLI: when working in this repository, you are expected to:

1. **Read this file before any non-trivial code change.** AGENTS.md will direct you here.
2. **When in doubt, prefer the most boring option that fits.**
3. **If a rule conflicts with the user's explicit instruction, the user wins** — but flag the conflict before silently complying.
4. **If you can't follow a rule because of a constraint** (e.g., the task explicitly requires a third-party dep), say so in the PR description, not silently.
5. **Don't vendor this guide elsewhere.** If you're working in a different repo, check that repo's STYLE.md (or absence thereof).

The guide evolves. If you find yourself repeatedly reaching for an exception, propose an edit instead of slowly drifting.
