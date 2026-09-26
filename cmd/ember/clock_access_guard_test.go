package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
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

// nonClockHTTPFiles build an http.Client for a host that is not the clock
// (weather and air providers, ICS feeds, the LaMetric icon gallery, GitHub
// releases, this server's own /healthz and /admin/doctor). Anything else that
// needs HTTP to the clock goes through clockAccess.
var nonClockHTTPFiles = []string{
	"clock_access.go", "weather.go", "meetings_poll.go", "icon_provision.go",
	"clock_health_http.go", "healthcheck.go", "doctor.go",
}

// The clock-access rules are structural, so they are checked on the type-
// checked package, not by name: an aliased import, a function value
// (mk := awtrix.NewClient) or a hand-built http.Client handed to
// discovery.Reachable all resolve to the same objects here.
func TestClockAccessIsTheOnlyWayToTheClock(t *testing.T) {
	fset, files, info := typeCheckPackage(t)
	for _, f := range files {
		name := filepath.Base(fset.Position(f.Pos()).Filename)
		for id, obj := range info.Uses {
			if fset.Position(id.Pos()).Filename != fset.Position(f.Pos()).Filename {
				continue
			}
			pos := fset.Position(id.Pos())
			fn, ok := obj.(*types.Func)
			if !ok {
				if tn, ok := obj.(*types.TypeName); ok && tn.Name() == "clockPublisher" && tn.Pkg() == info.pkg && name != "publisher.go" && name != "app.go" {
					t.Errorf("%s: clockPublisher outside publisher.go/app.go; the ungated publisher must stay behind quietPublisher", pos)
				}
				continue
			}
			recv := recvName(fn)
			pkg := ""
			if fn.Pkg() != nil {
				pkg = fn.Pkg().Path()
			}
			switch {
			// Only clockAccess.client constructs an awtrix client: one URL
			// rule, one timeout table.
			case pkg == awtrixPkg && recv == "" && fn.Name() == "NewClient" && name != "clock_access.go":
				t.Errorf("%s: awtrix.NewClient outside clock_access.go; use clockAccess.client/do/fetch", pos)
			// Reachability probes get the probe budget from clockAccess.reachable.
			case pkg == discoveryPkg && fn.Name() == "Reachable" && name != "clock_access.go":
				t.Errorf("%s: discovery.Reachable outside clock_access.go; use clockAccess.reachable", pos)
			// The coordinator is the only writer of pushed apps.
			case (recv != "" && slices.Contains([]string{"CustomApp", "ClearApp"}, fn.Name()) && pkg == info.pkg.Path() ||
				recv == "Client" && pkg == awtrixPkg && slices.Contains([]string{"PushApp", "DeleteApp"}, fn.Name())) &&
				!strings.HasPrefix(name, "coordinator") && name != "publisher.go" && name != "quiet_publisher.go":
				t.Errorf("%s: %s outside the coordinator; pushed apps have one writer", pos, fn.Name())
			// Sound on the raw client skips the quiet-hours gate. Only the
			// Publisher adapter and the menu's explicit test chime may.
			case recv == "Client" && pkg == awtrixPkg &&
				slices.Contains([]string{"Notify", "PlayRTTTL", "PlayMelody", "PlaySound"}, fn.Name()) &&
				name != "publisher.go" && name != "device_audio.go":
				t.Errorf("%s: awtrix %s outside publisher.go/device_audio.go bypasses the quiet-hours gate", pos, fn.Name())
			}
		}
		// A hand-built http.Client is how a new clock path would dodge the
		// timeout table.
		ast.Inspect(f, func(n ast.Node) bool {
			var typ types.Type
			switch n := n.(type) {
			case *ast.CompositeLit:
				typ = info.Types[n].Type
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 {
					typ = info.Types[n.Args[0]].Type
				}
			}
			if typ != nil && types.TypeString(typ, nil) == "net/http.Client" && !slices.Contains(nonClockHTTPFiles, name) {
				t.Errorf("%s: http.Client built outside clock_access.go; clock calls go through clockAccess (or add the file to nonClockHTTPFiles if the host isn't the clock)", fset.Position(n.Pos()))
			}
			return true
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

// typeCheckPackage parses this package's non-test files and type-checks them
// against the build cache's export data (go list -export).
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
