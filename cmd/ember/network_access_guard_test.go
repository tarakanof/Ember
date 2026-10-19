package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var networkAllowlist = map[string]int{
	"device_buttons.go:outboundIP:net.Dial":         1,
	"app.go:newServerApp:discovery.BrowseAWTRIX":    1,
	"main.go:main:discovery.Advertise":              1,
	"healthcheck.go:healthcheckOnce:http.Transport": 1,
	"healthcheck.go:runHealthcheck:http.Transport":  1,
}

var socketTypes = map[string]string{
	"net/http.Transport": "http.Transport",
	"net.Dialer":         "net.Dialer",
	"net.Resolver":       "net.Resolver",
	"net.ListenConfig":   "net.ListenConfig",
	"crypto/tls.Dialer":  "tls.Dialer",
}

var netSocketFuncs = []string{
	"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix",
	"ListenPacket", "ListenUDP", "ListenMulticastUDP", "ListenIP",
}

func TestNetworkAccessOnlyThroughTheGuardedTransport(t *testing.T) {
	fset, files, info := typeCheckPackage(t)
	for _, v := range networkBypassViolations(fset, files, info, networkAllowlist) {
		t.Error(v)
	}
}

func networkBypassViolations(fset *token.FileSet, files []*ast.File, info checkedInfo, allow map[string]int) []string {
	sites := map[string][]token.Pos{}
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		for _, decl := range f.Decls {
			scope := declName(decl)
			ast.Inspect(decl, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if what := socketUse(info.Uses[id]); what != "" {
						key := name + ":" + scope + ":" + what
						sites[key] = append(sites[key], id.Pos())
					}
				}
				return true
			})
		}
	}
	var out []string
	for _, key := range slices.Sorted(maps.Keys(sites)) {
		if len(sites[key]) <= allow[key] {
			continue
		}
		for _, pos := range sites[key] {
			out = append(out, fmt.Sprintf("%s: %s (%d uses, %d allowed); tests can't guard it. Use a client with a nil Transport, or inject it and add it to networkAllowlist", fset.Position(pos), key, len(sites[key]), allow[key]))
		}
	}
	return out
}

func declName(decl ast.Decl) string {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recv := fd.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func socketUse(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	path := obj.Pkg().Path()
	if tn, ok := obj.(*types.TypeName); ok {
		return socketTypes[path+"."+tn.Name()]
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return ""
	}
	if recv := recvName(fn); recv != "" {
		if what, ok := socketTypes[path+"."+recv]; ok && what != "http.Transport" {
			return what + "." + fn.Name()
		}
		return ""
	}
	switch path {
	case "net":
		if slices.Contains(netSocketFuncs, fn.Name()) || strings.HasPrefix(fn.Name(), "Lookup") || strings.HasPrefix(fn.Name(), "Resolve") {
			return "net." + fn.Name()
		}
	case "crypto/tls":
		if strings.HasPrefix(fn.Name(), "Dial") {
			return "tls." + fn.Name()
		}
	case discoveryPkg:
		if strings.HasPrefix(fn.Name(), "Browse") || fn.Name() == "Advertise" {
			return "discovery." + fn.Name()
		}
	}
	return ""
}

func TestNetworkAccessGuardCatchesEachBypass(t *testing.T) {
	const header = "package main\n\nimport (\n\t\"context\"\n\t\"crypto/tls\"\n\t\"net\"\n\t\"net/http\"\n\t\"time\"\n\n\t\"github.com/tarakanof/ember/internal/discovery\"\n)\n\n" +
		"var _ = context.Background\nvar _ = time.Second\nvar _ = discovery.PortFromAddr\nvar _ http.RoundTripper\nvar _ net.Conn\nvar _ tls.Config\n\n"
	allow := map[string]int{"device_buttons.go:outboundIP:net.Dial": 1}
	fset := token.NewFileSet()
	imp := exportImporter(t, fset)
	for _, tc := range []struct {
		name, file, src string
		want            int
	}{
		{"custom transport literal", "x.go", "var _ = &http.Transport{}", 1},
		{"custom transport value", "x.go", "var _ = http.Transport{}", 1},
		{"custom transport new", "x.go", "var _ = new(http.Transport)", 1},
		{"zero-value transport var", "x.go", "func f() http.RoundTripper { var tr http.Transport; return &tr }", 1},
		{"transport field", "x.go", "type probe struct{ tr *http.Transport }\n\nvar _ probe", 1},
		{"dialer literal", "x.go", "var _ = net.Dialer{Timeout: time.Second}", 1},
		{"zero-value dialer method", "x.go", `func f(ctx context.Context) { var d net.Dialer; _, _ = d.DialContext(ctx, "tcp", "192.0.2.1:80") }`, 2},
		{"dialer field", "x.go", "type probe struct{ d net.Dialer }\n\nvar _ probe", 1},
		{"resolver literal", "x.go", "var _ = &net.Resolver{}", 1},
		{"default resolver method", "x.go", `func f() { _, _ = net.DefaultResolver.LookupIPAddr(context.Background(), "clock.local") }`, 1},
		{"listen config method", "x.go", `func f(ctx context.Context) { var lc net.ListenConfig; _, _ = lc.ListenPacket(ctx, "udp", ":0") }`, 2},
		{"tcp dial", "x.go", `func f() { _, _ = net.Dial("tcp", "192.0.2.1:80") }`, 1},
		{"udp dial", "x.go", `func f() { _, _ = net.DialUDP("udp", nil, nil) }`, 1},
		{"udp listen", "x.go", `func f() { _, _ = net.ListenPacket("udp", ":0") }`, 1},
		{"multicast listen", "x.go", `func f() { _, _ = net.ListenMulticastUDP("udp", nil, nil) }`, 1},
		{"dns lookup", "x.go", `func f() { _, _ = net.LookupHost("clock.local") }`, 1},
		{"resolve addr", "x.go", `func f() { _, _ = net.ResolveUDPAddr("udp", "clock.local:80") }`, 1},
		{"tls dial", "x.go", `func f() { _, _ = tls.Dial("tcp", "192.0.2.1:443", nil) }`, 1},
		{"tls dialer", "x.go", `func f(ctx context.Context) { d := &tls.Dialer{}; _, _ = d.DialContext(ctx, "tcp", "192.0.2.1:443") }`, 2},
		{"browse reference", "x.go", "var _ = discovery.BrowseAWTRIX", 1},
		{"browse call", "x.go", `func f() { _, _ = discovery.BrowseEmber(context.Background(), time.Second) }`, 1},
		{"advertise", "x.go", `func f() { _ = discovery.Advertise(context.Background(), "x", 1, "v") }`, 1},
		{"allowlisted dial", "device_buttons.go", `func outboundIP() { _, _ = net.Dial("udp", "192.0.2.1:80") }`, 0},
		{"second dial at the allowlisted site", "device_buttons.go", `func outboundIP() { _, _ = net.Dial("udp", "a:80"); _, _ = net.Dial("udp", "b:80") }`, 2},
		{"dial elsewhere in the allowlisted file", "device_buttons.go", `func other() { _, _ = net.Dial("udp", "192.0.2.1:80") }`, 1},
		{"allowlist is per file", "app.go", `func outboundIP() { _, _ = net.Dial("udp", "192.0.2.1:80") }`, 1},
		{"default client", "x.go", "var _ = &http.Client{Timeout: time.Second}", 0},
		{"inbound listener", "x.go", `func f() { _, _ = net.Listen("tcp", "127.0.0.1:0") }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile(fset, tc.file, header+tc.src+"\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
			pkg, err := (&types.Config{Importer: imp}).Check("example.com/netfixture", fset, []*ast.File{f}, info)
			if err != nil {
				t.Fatalf("type-check: %v", err)
			}
			got := networkBypassViolations(fset, []*ast.File{f}, checkedInfo{info, pkg}, allow)
			if len(got) != tc.want {
				t.Fatalf("violations = %d %q, want %d", len(got), got, tc.want)
			}
		})
	}
}

func TestOnlyTestMainWritesTheDefaultTransport(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, n := range names {
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{Importer: exportImporter(t, fset, "-test")}).Check("github.com/tarakanof/ember/cmd/ember", fset, files, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}
	if got := defaultTransportWrites(fset, files, info); len(got) != 1 || !strings.HasPrefix(got[0], "env_test.go:") || !strings.HasSuffix(got[0], " in TestMain") {
		t.Fatalf("http.DefaultTransport writes = %q, want only TestMain's: a later write races goroutines leaked from earlier tests (#594)", got)
	}
}

func TestDefaultTransportWritesCatchesEachForm(t *testing.T) {
	const src = `package main

import (
	"net/http"
	nh "net/http"
	. "net/http"
)

func TestMain() { http.DefaultTransport = nil }

func swap() { var a int; a, http.DefaultTransport = 1, nil; _ = a }

func viaPointer() { p := &http.DefaultTransport; *p = nil }

func derefAddr() { *(&http.DefaultTransport) = nil }

func paren() { (http.DefaultTransport) = nil }

func aliased() { nh.DefaultTransport = nil }

func read() http.RoundTripper { return http.DefaultTransport }

func shadowed() { var http struct{ DefaultTransport int }; http.DefaultTransport = 1; _ = http }

func dotted() { DefaultTransport = nil }

func dottedPointer() { p := &DefaultTransport; *p = nil }

func dotShadowed() { var DefaultTransport int; DefaultTransport = 1; _ = DefaultTransport }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{Importer: exportImporter(t, fset)}).Check("example.com/transportfixture", fset, []*ast.File{f}, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}
	var fns []string
	for _, w := range defaultTransportWrites(fset, []*ast.File{f}, info) {
		_, fn, _ := strings.Cut(w, " in ")
		fns = append(fns, fn)
	}
	if want := []string{"TestMain", "swap", "viaPointer", "derefAddr", "paren", "aliased", "dotted", "dottedPointer"}; !slices.Equal(fns, want) {
		t.Fatalf("writes in %q, want %q", fns, want)
	}
}

func defaultTransportWrites(fset *token.FileSet, files []*ast.File, info *types.Info) []string {
	isDefault := func(e ast.Expr) bool {
		var id *ast.Ident
		switch e := ast.Unparen(e).(type) {
		case *ast.Ident:
			id = e
		case *ast.SelectorExpr:
			id = e.Sel
		default:
			return false
		}
		v, ok := info.Uses[id].(*types.Var)
		return ok && v.Pkg() != nil && v.Pkg().Path() == "net/http" && v.Name() == "DefaultTransport"
	}
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			ast.Inspect(decl, func(n ast.Node) bool {
				var targets []ast.Expr
				switch n := n.(type) {
				case *ast.AssignStmt:
					targets = n.Lhs
				case *ast.UnaryExpr:
					if n.Op == token.AND {
						targets = []ast.Expr{n.X}
					}
				}
				for _, e := range targets {
					if isDefault(e) {
						out = append(out, fmt.Sprintf("%s in %s", fset.Position(e.Pos()), declName(decl)))
					}
				}
				return true
			})
		}
	}
	return out
}
