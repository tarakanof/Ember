package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const openAPIPath = "../../docs/openapi.yaml"

var internalRoutes = []string{
	"GET /metrics",
	"GET /admin/doctor",
	"POST /admin/reload",
	"POST /hooks/awtrix/button",
	"POST " + bootHookPath,
	"POST /hooks/plex",
}

type specAuth struct {
	public       bool
	owner        bool
	device       bool
	clientScopes []string
}

type specOp struct {
	route string
	auth  specAuth
}

var (
	specPathRe     = regexp.MustCompile(`^  (/[^:]*):\s*$`)
	specMethodRe   = regexp.MustCompile(`^    (get|put|post|delete|patch):\s*$`)
	specSecurityRe = regexp.MustCompile(`^      security: (\[.*\])\s*$`)
	specSchemeRe   = regexp.MustCompile(`\{(\w+): \[([^\]]*)\]\}`)
)

func readSpecOps(t *testing.T) []specOp {
	t.Helper()
	f, err := os.Open(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	var ops []specOp
	inPaths, path := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && line != "" {
			inPaths = line == "paths:"
			continue
		}
		if !inPaths {
			continue
		}
		if m := specPathRe.FindStringSubmatch(line); m != nil {
			path = m[1]
			continue
		}
		if m := specMethodRe.FindStringSubmatch(line); m != nil {
			ops = append(ops, specOp{route: strings.ToUpper(m[1]) + " " + path})
			continue
		}
		if m := specSecurityRe.FindStringSubmatch(line); m != nil && len(ops) > 0 {
			op := &ops[len(ops)-1]
			if m[1] == "[]" {
				op.auth.public = true
			}
			for _, s := range specSchemeRe.FindAllStringSubmatch(m[1], -1) {
				switch s[1] {
				case "ownerToken":
					op.auth.owner = true
				case "deviceToken":
					op.auth.device = true
				case "clientToken":
					op.auth.clientScopes = append(op.auth.clientScopes, strings.TrimSpace(s[2]))
				default:
					t.Fatalf("%s: unknown security scheme %q", op.route, s[1])
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		a := op.auth
		if !a.public && !a.owner && !a.device && len(a.clientScopes) == 0 {
			t.Errorf("%s: no one-line security requirement", op.route)
		}
	}
	if len(ops) < 50 {
		t.Fatalf("read only %d operations from %s; did the layout change?", len(ops), openAPIPath)
	}
	return ops
}

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	_, patterns := newTestApp(t).routeTable()
	var out []string
	for _, p := range patterns {
		if strings.Contains(p, " ") {
			out = append(out, p)
		}
	}
	return out
}

func TestOpenAPICoversEveryRoute(t *testing.T) {
	ops := readSpecOps(t)
	inSpec := map[string]bool{}
	for _, op := range ops {
		if inSpec[op.route] {
			t.Errorf("%s: listed twice in the spec", op.route)
		}
		inSpec[op.route] = true
	}
	routes := registeredRoutes(t)
	for _, r := range routes {
		if !inSpec[r] && !slices.Contains(internalRoutes, r) {
			t.Errorf("route %q is neither in docs/openapi.yaml nor in internalRoutes", r)
		}
		if inSpec[r] && slices.Contains(internalRoutes, r) {
			t.Errorf("route %q is in the spec and in internalRoutes", r)
		}
	}
	for _, op := range ops {
		if !slices.Contains(routes, op.route) {
			t.Errorf("docs/openapi.yaml lists %q, which the server does not register", op.route)
		}
	}
}

func TestOpenAPIAuthMatchesServer(t *testing.T) {
	ops := readSpecOps(t)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.RateLimit.Disabled = true })
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	knob := mintKnob(t, srv, http.StatusCreated)

	type cred struct {
		name, token string
		owner       bool
		device      bool
		scopes      []string
	}
	creds := []cred{
		{name: "none"},
		{name: "wrong", token: "wrong"},
		{name: "master", token: testToken, owner: true},
		{name: "knob", token: knob.Token, device: true},
	}
	for _, s := range []string{scopeIngest, scopeControl, scopeRead, scopeAdmin} {
		creds = append(creds, cred{name: s, token: mintClient(t, srv, s, s).Token, scopes: []string{s}})
	}

	for _, op := range ops {
		if op.auth.public {
			continue
		}
		method, path, _ := strings.Cut(op.route, " ")
		path = strings.NewReplacer("{id}", knob.ID, "{dump}", "1a2b3c4d", "{version}", "0.9.14", "{build}", "a1b2c3d4").Replace(path)
		for _, c := range creds {
			want := 0
			switch {
			case c.owner && op.auth.owner, c.device && op.auth.device:
			case c.scopes != nil && len(op.auth.clientScopes) > 0:
				if !slices.ContainsFunc(op.auth.clientScopes, func(s string) bool { return scopeAllowed(c.scopes, s) }) {
					want = http.StatusForbidden
				}
			default:
				want = http.StatusUnauthorized
			}
			if want == 0 {
				if len(c.scopes) == 1 && c.scopes[0] != scopeAdmin && slices.Contains(op.auth.clientScopes, c.scopes[0]) {
					t.Run(op.route+" as "+c.name+" admitted", func(t *testing.T) {
						resp, b := devReq(t, srv, method, path, c.token, "")
						if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
							t.Fatalf("status = %d, want admitted: %s", resp.StatusCode, b)
						}
					})
				}
				continue
			}
			t.Run(op.route+" as "+c.name, func(t *testing.T) {
				resp, b := devReq(t, srv, method, path, c.token, "")
				if resp.StatusCode != want {
					t.Fatalf("status = %d, want %d: %s", resp.StatusCode, want, b)
				}
			})
		}
	}
}

func TestOpenAPIExternalExamplesExist(t *testing.T) {
	b, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`externalValue: (\S+)`).FindAllStringSubmatch(string(b), -1)
	if len(refs) == 0 {
		t.Fatal("no externalValue examples; the dashboard goldens should be referenced")
	}
	for _, m := range refs {
		if _, err := os.Stat(filepath.Join(filepath.Dir(openAPIPath), m[1])); err != nil {
			t.Errorf("example %s: %v", m[1], err)
		}
	}
}
