package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var networkAllowlist = map[string][]string{
	"device_buttons.go": {"net.Dial"},
	"app.go":            {"discovery.BrowseAWTRIX"},
	"main.go":           {"discovery.Advertise"},
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

func networkBypassViolations(fset *token.FileSet, files []*ast.File, info checkedInfo, allow map[string][]string) []string {
	var out []string
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		report := func(pos token.Pos, what string) {
			if slices.Contains(allow[name], what) {
				return
			}
			out = append(out, fmt.Sprintf("%s: %s outside the allowlist; tests can't guard it. Use a client with a nil Transport (or clone http.DefaultTransport), or inject it", fset.Position(pos), what))
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				if what := socketType(info.Types[n].Type); what != "" {
					report(n.Pos(), what)
				}
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 {
					if what := socketType(info.Types[n.Args[0]].Type); what != "" {
						report(n.Pos(), what)
					}
				}
			case *ast.Ident:
				if what := socketFunc(info.Uses[n]); what != "" {
					report(n.Pos(), what)
				}
			}
			return true
		})
	}
	return out
}

func socketType(typ types.Type) string {
	if typ == nil {
		return ""
	}
	switch s := types.TypeString(typ, nil); s {
	case "net/http.Transport":
		return "http.Transport"
	case "net.Dialer", "net.Resolver":
		return s
	}
	return ""
}

func socketFunc(obj types.Object) string {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	switch fn.Pkg().Path() {
	case "net":
		if recvName(fn) == "" && slices.Contains(netSocketFuncs, fn.Name()) || strings.HasPrefix(fn.Name(), "Lookup") {
			return "net." + fn.Name()
		}
	case discoveryPkg:
		if strings.HasPrefix(fn.Name(), "Browse") || fn.Name() == "Advertise" {
			return "discovery." + fn.Name()
		}
	}
	return ""
}

func TestNetworkAccessGuardCatchesEachBypass(t *testing.T) {
	const header = "package main\n\nimport (\n\t\"context\"\n\t\"net\"\n\t\"net/http\"\n\t\"time\"\n\n\t\"github.com/tarakanof/ember/internal/discovery\"\n)\n\n" +
		"var _ = context.Background\nvar _ = time.Second\nvar _ = discovery.PortFromAddr\nvar _ http.RoundTripper\nvar _ net.Conn\n\n"
	fset := token.NewFileSet()
	imp := exportImporter(t, fset)
	for _, tc := range []struct {
		name, file, src string
		want            int
	}{
		{"custom transport literal", "x.go", "var _ = &http.Transport{}", 1},
		{"custom transport value", "x.go", "var _ = http.Transport{}", 1},
		{"custom transport new", "x.go", "var _ = new(http.Transport)", 1},
		{"dialer", "x.go", "var _ = net.Dialer{Timeout: time.Second}", 1},
		{"resolver", "x.go", "var _ = &net.Resolver{}", 1},
		{"tcp dial", "x.go", `func f() { _, _ = net.Dial("tcp", "192.0.2.1:80") }`, 1},
		{"udp dial", "x.go", `func f() { _, _ = net.DialUDP("udp", nil, nil) }`, 1},
		{"udp listen", "x.go", `func f() { _, _ = net.ListenPacket("udp", ":0") }`, 1},
		{"multicast listen", "x.go", `func f() { _, _ = net.ListenMulticastUDP("udp", nil, nil) }`, 1},
		{"dns lookup", "x.go", `func f() { _, _ = net.LookupHost("clock.local") }`, 1},
		{"resolver method", "x.go", `func f() { _, _ = net.DefaultResolver.LookupIPAddr(context.Background(), "clock.local") }`, 1},
		{"browse reference", "x.go", "var _ = discovery.BrowseAWTRIX", 1},
		{"browse call", "x.go", `func f() { _, _ = discovery.BrowseEmber(context.Background(), time.Second) }`, 1},
		{"advertise", "x.go", `func f() { _ = discovery.Advertise(context.Background(), "x", 1, "v") }`, 1},
		{"allowlisted dial", "device_buttons.go", `func f() { _, _ = net.Dial("udp", "192.0.2.1:80") }`, 0},
		{"allowlist is per file", "app.go", `func f() { _, _ = net.Dial("udp", "192.0.2.1:80") }`, 1},
		{"default client", "x.go", "var _ = &http.Client{Timeout: time.Second}", 0},
		{"cloned default transport", "x.go", "func f(b *http.Transport) http.RoundTripper { return b.Clone() }", 0},
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
			got := networkBypassViolations(fset, []*ast.File{f}, checkedInfo{info, pkg}, networkAllowlist)
			if len(got) != tc.want {
				t.Fatalf("violations = %d %q, want %d", len(got), got, tc.want)
			}
		})
	}
}
