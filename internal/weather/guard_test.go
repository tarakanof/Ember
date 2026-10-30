package weather

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

var refusedHosts atomic.Int64

type loopbackOnly struct{ http.RoundTripper }

func (l loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return l.RoundTripper.RoundTrip(r)
	}
	refusedHosts.Add(1)
	return nil, fmt.Errorf("test reached for %s: weather tests stay on loopback", host)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = loopbackOnly{http.DefaultTransport}
	code := m.Run()
	if n := refusedHosts.Load(); n > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "%d requests left loopback\n", n)
		code = 1
	}
	os.Exit(code)
}

func TestFetcherOpensNoSocketsOfItsOwn(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "net" || path == "crypto/tls" {
				t.Errorf("%s imports %s; requests go through the default transport", n, path)
			}
		}
		ast.Inspect(f, func(x ast.Node) bool {
			if s, ok := x.(*ast.SelectorExpr); ok && s.Sel.Name == "Transport" {
				if id, ok := s.X.(*ast.Ident); ok && id.Name == "http" {
					t.Errorf("%s: http.Transport; requests go through the default transport", fset.Position(s.Pos()))
				}
			}
			return true
		})
	}
}
