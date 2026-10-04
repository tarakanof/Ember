package producer

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrintSetupHintsHeadlessWarnsAboutTokenAndShowsDiscovery(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	local := DiscoveredServer{Name: "Ember", Host: "unraid.local.", URL: ok.URL}
	var b bytes.Buffer
	PrintSetupHints(&b, SetupHintsInput{
		Source: "build-1", Token: TokenPlaceholder, Configured: "", Home: t.TempDir(), Headless: true, Discover: true,
		Browse: fakeBrowser([]DiscoveredServer{local}, nil, nil),
	})
	got := b.String()
	for _, want := range []string{"Headless mode", `"build-1"`, "WARNING: EMBER_TOKEN is not set", "found 1", "using " + ok.URL, "reachable"} {
		if !strings.Contains(got, want) {
			t.Errorf("hints missing %q:\n%s", want, got)
		}
	}
}

func TestPrintSetupHintsWithoutDiscoverStaysOffline(t *testing.T) {
	var b bytes.Buffer
	browsed := false
	PrintSetupHints(&b, SetupHintsInput{
		Source: "mbp", Token: "t", Configured: "", Home: t.TempDir(),
		Browse: func(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error) {
			browsed = true
			return nil, nil
		},
	})
	if browsed {
		t.Fatal("configure-time hints browsed the network")
	}
	if !strings.Contains(b.String(), "run `discover` or `doctor`") {
		t.Errorf("no discover hint:\n%s", b.String())
	}
}
