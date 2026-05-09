package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"
)

const pollInterval = 2 * time.Second

func runApp() {
	systray.Run(onSystrayReady, onSystrayExit)
}

var (
	statusItem  *systray.MenuItem
	countItem   *systray.MenuItem
	openStateMI *systray.MenuItem
	prefsMI     *systray.MenuItem
	doctorMI    *systray.MenuItem
	reloadMI    *systray.MenuItem
	aboutMI     *systray.MenuItem
	quitMI      *systray.MenuItem

	prefsSrv *prefsServer
	stateURL atomic.Value // string

	menuMu sync.Mutex // serializes concurrent updateMenu calls
)

func onSystrayReady() {
	systray.SetTemplateIcon(iconForState("idle"), iconForState("idle"))
	systray.SetTooltip("AWTRIX: idle")

	statusItem = systray.AddMenuItem("Loading…", "")
	statusItem.Disable()
	countItem = systray.AddMenuItem("0 active sessions", "")
	countItem.Disable()
	systray.AddSeparator()
	openStateMI = systray.AddMenuItem("Open server /state in browser", "")
	prefsMI = systray.AddMenuItem("Preferences…", "")
	doctorMI = systray.AddMenuItem("Doctor", "")
	reloadMI = systray.AddMenuItem("Reload", "")
	systray.AddSeparator()
	aboutMI = systray.AddMenuItem("About awtrix-menu", "")
	quitMI = systray.AddMenuItem("Quit", "")

	home, _ := os.UserHomeDir()
	envPath := filepath.Join(home, ".config", "awtrix-ai-status", "producer.env")
	prefsSrv = newPrefsServer(envPath)

	// Click handlers
	go func() {
		for {
			select {
			case <-openStateMI.ClickedCh:
				if u := stateURL.Load(); u != nil {
					startAndReap(exec.Command("open", u.(string)+"/state"))
				}
			case <-prefsMI.ClickedCh:
				url := prefsSrv.urlForClick()
				if url != "" {
					startAndReap(exec.Command("open", url))
				}
			case <-doctorMI.ClickedCh:
				go openDoctor()
			case <-reloadMI.ClickedCh:
				go updateMenu(envPath)
			case <-aboutMI.ClickedCh:
				startAndReap(exec.Command("open", "https://github.com/tarakanof/awtrix-ai-status"))
			case <-quitMI.ClickedCh:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = prefsSrv.shutdown(ctx)
				cancel()
				systray.Quit()
				return
			}
		}
	}()

	// Polling goroutine
	go func() {
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			updateMenu(envPath)
			<-t.C
		}
	}()
}

func onSystrayExit() {
	os.Exit(0)
}

func updateMenu(envPath string) {
	menuMu.Lock()
	defer menuMu.Unlock()
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".local", "state", "awtrix-ai-status", "sessions")
	rec, _ := readEnv(envPath)
	if rec == nil {
		rec = &envRec{}
	}
	ttl := ttlFromEnv(rec)
	view := readView(stateDir, ttl)

	// Status text
	statusText := "AWTRIX: idle"
	tip := "AWTRIX: idle"
	switch view.DominantState {
	case "running":
		statusText = "Claude — running" + suffix(view.LastMessage)
		tip = "AWTRIX: Claude running" + suffix(view.LastMessage)
	case "waiting":
		statusText = "Claude — waiting" + suffix(view.LastMessage)
		tip = "AWTRIX: waiting for approval"
	case "error":
		statusText = "Claude — error" + suffix(view.LastMessage)
		tip = "AWTRIX: error" + suffix(view.LastMessage)
	case "done":
		// done has no dedicated icon (spec ships only 4: idle/running/waiting/error),
		// so the icon falls back to idle while the text reflects the recent finish.
		statusText = "Claude — done" + suffix(view.LastMessage)
		tip = "AWTRIX: done" + suffix(view.LastMessage)
	}
	statusItem.SetTitle(statusText)
	countItem.SetTitle(fmt.Sprintf("%d active session(s)", view.ActiveCount))
	systray.SetTooltip(tip)

	// Icon
	icon := iconForState(view.DominantState)
	if view.DominantState == "" {
		icon = iconForState("idle")
	}
	systray.SetTemplateIcon(icon, icon)

	// Open /state availability
	url := rec.get("STATUS_SERVER_URL")
	stateURL.Store(url)
	if url == "" {
		openStateMI.Disable()
	} else {
		openStateMI.Enable()
	}
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " — " + s
}

// startAndReap starts cmd and reaps it in a background goroutine so the
// menu-bar process doesn't accumulate zombie children over its lifetime.
func startAndReap(cmd *exec.Cmd) {
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

func openDoctor() {
	// Resolve the producer binary's absolute path. The LaunchAgent's PATH
	// (/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin) doesn't include
	// ~/go/bin, and Terminal's user shell may not either, so we look it up
	// here and pass the absolute path into the AppleScript.
	binPath := findProducerBin()
	var script string
	if binPath == "" {
		script = `tell application "Terminal" to do script "echo 'awtrix-claude-producer not found in PATH or ~/go/bin. Install it via apps/awtrix-claude-producer/install.sh first.'; echo; echo Press any key to close.; read -n 1"`
	} else {
		// Single-quote the binary path for the shell so paths with spaces work;
		// the AppleScript "do script" runs the string in a shell, so shell
		// quoting applies here.
		script = fmt.Sprintf(`tell application "Terminal" to do script "'%s' doctor; echo; echo Press any key to close.; read -n 1"`, binPath)
	}
	_ = exec.Command("osascript", "-e", script).Run()
}

// findProducerBin returns the absolute path to awtrix-claude-producer if it
// can be located, or "" if not found. Searches the menu's own PATH (covers
// /opt/homebrew/bin and /usr/local/bin) plus the conventional Go install
// locations a `go install` would land in.
func findProducerBin() string {
	if p, err := exec.LookPath("awtrix-claude-producer"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{
		os.Getenv("GOBIN"),
		filepath.Join(os.Getenv("GOPATH"), "bin"),
		filepath.Join(home, "go", "bin"),
	} {
		if dir == "" || dir == "/bin" { // skip empty / GOPATH-was-empty
			continue
		}
		path := filepath.Join(dir, "awtrix-claude-producer")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
