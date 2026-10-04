package producer

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"
)

// SetupHintsInput is what install/configure report after setting up.
type SetupHintsInput struct {
	Source     string
	Token      string
	Configured string // EMBER_SERVER_URL as written
	Prefer     string // EMBER_SERVER_INSTANCE
	Home       string
	Headless   bool
	Browse     ServerBrowser // MDNSBrowser when nil
}

// PrintSetupHints prints the post-install checklist: mode, source, token and
// the server (discovering it when EMBER_SERVER_URL is empty or "auto").
func PrintSetupHints(w io.Writer, in SetupHintsInput) {
	switch {
	case in.Headless && runtime.GOOS == "darwin":
		fmt.Fprintln(w, "Headless mode: no Ember.app, so this CLI owns the LaunchAgent and finds the server itself.")
	case in.Headless:
		fmt.Fprintln(w, "Headless mode: this CLI owns the systemd --user unit and finds the server itself.")
	default:
		fmt.Fprintln(w, "Ember.app is installed: it normally runs this producer (Settings › Agents); this CLI install is for dev builds.")
	}
	fmt.Fprintln(w, SourceHint(in.Source))
	if h := TokenHint(in.Token); h != "" {
		fmt.Fprintln(w, "WARNING: "+h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*DefaultBrowseTimeout+time.Second)
	defer cancel()
	for _, l := range ServerReport(ctx, ServerReportInput{Configured: in.Configured, Prefer: in.Prefer, Home: in.Home, Browse: in.Browse}) {
		fmt.Fprintln(w, l)
	}
}
