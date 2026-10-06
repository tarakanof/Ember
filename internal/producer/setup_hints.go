package producer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"
	"time"
)

type SetupHintsInput struct {
	Source     string
	Token      string
	Configured string
	Prefer     string
	Home       string
	Headless   bool
	Discover   bool
	LingerUser string
	Run        Runner
	Browse     ServerBrowser
}

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
	if in.LingerUser != "" && runtime.GOOS == "linux" {
		run := in.Run
		if run == nil {
			run = ExecRunner
		}
		if h := LingerHint(run, in.LingerUser); h != "" {
			fmt.Fprintln(w, h)
		}
	}
	if !in.Discover {
		if IsAutoServerURL(in.Configured) {
			fmt.Fprintln(w, "server_url: auto (mDNS _ember._tcp); run `discover` or `doctor` to find and cache it now (the daemon also finds it on start)")
		} else {
			fmt.Fprintln(w, "server_url: "+in.Configured)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*DefaultBrowseTimeout+time.Second)
	defer cancel()
	for _, l := range ServerReport(ctx, ServerReportInput{Configured: in.Configured, Prefer: in.Prefer, Home: in.Home, Browse: in.Browse}) {
		fmt.Fprintln(w, l)
	}
}

func CurrentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func PrintDiscover(w io.Writer, configured, prefer, home string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*DefaultBrowseTimeout+time.Second)
	defer cancel()
	for _, l := range ServerReport(ctx, ServerReportInput{Configured: configured, Prefer: prefer, Home: home}) {
		fmt.Fprintln(w, l)
	}
}
