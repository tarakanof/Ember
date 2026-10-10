package main

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	awtrixPkg    = "github.com/tarakanof/ember/internal/awtrix"
	discoveryPkg = "github.com/tarakanof/ember/internal/discovery"
)

var nonClockHTTPFiles = []string{
	"clock_access.go", "weather.go", "meetings_poll.go", "icon_provision.go",
	"clock_health_http.go", "healthcheck.go", "doctor.go", "nowplaying_plex.go", "nowplaying_deezer.go",
}

func TestClockAccessIsTheOnlyWayToTheClock(t *testing.T) {
	fset, files, info := typeCheckPackage(t)
	for _, v := range clockAccessViolations(fset, files, info) {
		t.Error(v)
	}
}

func clockAccessViolations(fset *token.FileSet, files []*ast.File, info checkedInfo) []string {
	var out []string
	report := func(pos token.Pos, format string, args ...any) {
		out = append(out, fmt.Sprintf("%s: "+format, append([]any{fset.Position(pos)}, args...)...))
	}
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		for _, decl := range f.Decls {
			inPublisher := name == "publisher.go" && isPublisherMethod(decl, info)
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.Ident:
					checkClockUse(n, name, inPublisher, info, report)
				case *ast.CompositeLit:
					checkHTTPClient(n, info.Types[n].Type, name, report)
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 {
						checkHTTPClient(n, info.Types[n.Args[0]].Type, name, report)
					}
				}
				return true
			})
		}
	}
	return out
}

func isPublisherMethod(decl ast.Decl, info checkedInfo) bool {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
		return false
	}
	iface, ok := info.pkg.Scope().Lookup("Publisher").(*types.TypeName)
	if !ok {
		return false
	}
	recv := info.Types[fd.Recv.List[0].Type].Type
	return recv != nil && types.Implements(recv, iface.Type().Underlying().(*types.Interface))
}

func checkHTTPClient(n ast.Node, typ types.Type, name string, report func(token.Pos, string, ...any)) {
	if typ != nil && types.TypeString(typ, nil) == "net/http.Client" && !slices.Contains(nonClockHTTPFiles, name) {
		report(n.Pos(), "http.Client built outside clock_access.go; clock calls go through clockAccess (or add the file to nonClockHTTPFiles if the host isn't the clock)")
	}
}

func checkClockUse(id *ast.Ident, name string, inPublisher bool, info checkedInfo, report func(token.Pos, string, ...any)) {
	obj := info.Uses[id]
	if obj == nil {
		return
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		if tn, ok := obj.(*types.TypeName); ok && tn.Name() == "clockPublisher" && tn.Pkg() == info.pkg && name != "publisher.go" && name != "app.go" {
			report(id.Pos(), "clockPublisher outside publisher.go/app.go; NewApp builds the one clock publisher")
		}
		return
	}
	recv := recvName(fn)
	pkg := ""
	if fn.Pkg() != nil {
		pkg = fn.Pkg().Path()
	}
	switch {
	case pkg == awtrixPkg && recv == "" && fn.Name() == "NewClient" && name != "clock_access.go":
		report(id.Pos(), "awtrix.NewClient outside clock_access.go; use clockAccess.client/do/fetch")
	case pkg == discoveryPkg && fn.Name() == "Reachable" && name != "clock_access.go":
		report(id.Pos(), "discovery.Reachable outside clock_access.go; use clockAccess.reachable")
	case (recv != "" && slices.Contains([]string{"CustomApp", "ClearApp"}, fn.Name()) && pkg == info.pkg.Path() ||
		recv == "Client" && pkg == awtrixPkg && slices.Contains([]string{"PushApp", "DeleteApp"}, fn.Name())) &&
		!strings.HasPrefix(name, "coordinator") && !inPublisher:
		report(id.Pos(), "%s outside the coordinator; pushed apps have one writer", fn.Name())
	case recv == "Client" && pkg == awtrixPkg &&
		slices.Contains([]string{"Notify", "PlayRTTTL", "PlayMelody", "PlaySound"}, fn.Name()) &&
		!inPublisher && name != "device_audio.go":
		report(id.Pos(), "awtrix %s outside Publisher methods/device_audio.go bypasses the quiet-hours gate", fn.Name())
	case recv != "" && pkg == info.pkg.Path() &&
		slices.Contains([]string{"Notify", "DismissNotifyByName", "PlayRTTTL"}, fn.Name()) &&
		name != "coordinator_notices.go" && !inPublisher:
		report(id.Pos(), "%s outside coordinator_notices.go; popups and chimes go through notices", fn.Name())
	}
}

const guardFixtureBase = `package main

type Publisher interface {
	Notify() error
	CustomApp() error
}

type clockPublisher struct{}

func (clockPublisher) Notify() error    { return nil }
func (clockPublisher) CustomApp() error { return nil }

type relay struct{ p Publisher }
`

func TestClockAccessGuardScopesPublisherExemption(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		want            int
	}{
		{"publisher method", "publisher.go", "func (c clockPublisher) Again() error { return c.Notify() }", 0},
		{"publisher method closure", "publisher.go", "func (c clockPublisher) Later() func() error { return func() error { return c.CustomApp() } }", 0},
		{"free func in publisher.go", "publisher.go", "func sneak(p Publisher) error { return p.Notify() }", 1},
		{"free func push in publisher.go", "publisher.go", "func sneakPush(p Publisher) error { return p.CustomApp() }", 1},
		{"non-Publisher method in publisher.go", "publisher.go", "func (r relay) send() error { return r.p.Notify() }", 1},
		{"publisher method elsewhere", "other.go", "func (c clockPublisher) Again() error { return c.Notify() }", 2},
		{"notices file", "coordinator_notices.go", "func show(p Publisher) error { return p.Notify() }", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcs := map[string]string{"publisher.go": guardFixtureBase}
			srcs[tc.file] = cmp.Or(srcs[tc.file], "package main\n") + "\n" + tc.src + "\n"
			fset := token.NewFileSet()
			var files []*ast.File
			for _, name := range slices.Sorted(maps.Keys(srcs)) {
				f, err := parser.ParseFile(fset, name, srcs[name], 0)
				if err != nil {
					t.Fatal(err)
				}
				files = append(files, f)
			}
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
			pkg, err := (&types.Config{}).Check("example.com/guardfixture", fset, files, info)
			if err != nil {
				t.Fatalf("type-check: %v", err)
			}
			got := clockAccessViolations(fset, files, checkedInfo{info, pkg})
			if len(got) != tc.want {
				t.Fatalf("violations = %d %q, want %d", len(got), got, tc.want)
			}
		})
	}
}

func recvName(fn *types.Func) string {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return ""
	}
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return "?"
}

type checkedInfo struct {
	*types.Info
	pkg *types.Package
}

func typeCheckPackage(t *testing.T) (*token.FileSet, []*ast.File, checkedInfo) {
	t.Helper()
	out, err := exec.Command("go", "list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -export: %v", err)
	}
	exports := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if path, file, ok := strings.Cut(sc.Text(), "\t"); ok && file != "" {
			exports[path] = file
		}
	}
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
	pkg, err := (&types.Config{Importer: imp}).Check("github.com/tarakanof/ember/cmd/ember", fset, files, info)
	if err != nil {
		t.Fatalf("type-check: %v", err)
	}
	return fset, files, checkedInfo{info, pkg}
}
