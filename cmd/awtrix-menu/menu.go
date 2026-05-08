package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
					_ = exec.Command("open", u.(string)+"/state").Start()
				}
			case <-prefsMI.ClickedCh:
				url := prefsSrv.urlForClick()
				if url != "" {
					_ = exec.Command("open", url).Start()
				}
			case <-doctorMI.ClickedCh:
				go openDoctor()
			case <-reloadMI.ClickedCh:
				go updateMenu(envPath)
			case <-aboutMI.ClickedCh:
				_ = exec.Command("open", "https://github.com/tarakanof/awtrix-ai-status").Start()
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

func openDoctor() {
	// Ask Terminal to run the producer's doctor command
	script := `tell application "Terminal" to do script "awtrix-claude-producer doctor; echo; echo Press any key to close.; read -n 1"`
	_ = exec.Command("osascript", "-e", script).Run()
}
