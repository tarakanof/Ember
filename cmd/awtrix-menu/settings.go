package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/systray"
)

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validateSetting validates and normalizes a single producer.env value,
// returning the value to persist or an error explaining the rejection.
// The token's "blank = keep current" rule is the caller's responsibility,
// not this function's.
func validateSetting(key, value string) (string, error) {
	v := strings.TrimSpace(value)
	if strings.ContainsAny(v, ctrlChars) {
		return "", fmt.Errorf("value may not contain control characters")
	}
	switch key {
	case "STATUS_SOURCE":
		if v == "" {
			return "", fmt.Errorf("source must not be empty")
		}
		return v, nil
	case "STATUS_SERVER_URL":
		if v == "" {
			return "", fmt.Errorf("server URL must not be empty")
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", fmt.Errorf("must be an http(s) URL with a host and no embedded credentials")
		}
		return v, nil
	case "STATUS_TOKEN":
		return v, nil // blank-handling is the caller's job
	case "STATUS_SOURCE_COLOR":
		if v == "" {
			return "", nil // unset = no tint
		}
		if !hexColorRe.MatchString(v) {
			return "", fmt.Errorf("color must be #RRGGBB hex")
		}
		return v, nil
	case "STATUS_CONTEXT_WINDOW_TOKENS":
		if v == "" {
			return "", nil // unset = model default
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return "", fmt.Errorf("context window must be a non-negative integer")
		}
		return strconv.Itoa(n), nil
	default:
		return v, nil
	}
}

// isEnvTrue mirrors the producer's STATUS_CONTEXT_PCT_ENABLED parsing:
// default true; only false/0/no/off (case-insensitive) disable it.
func isEnvTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return false
	default:
		return true // includes "" — the producer's default is true
	}
}

// escapeAppleScript escapes a Go string for embedding inside an
// AppleScript double-quoted literal.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// runInputDialog shows a native text-input dialog pre-filled with def.
// Returns (value, true) on OK; ("", false) on Cancel or any error.
func runInputDialog(prompt, def string) (string, bool) {
	script := fmt.Sprintf(
		`set r to display dialog "%s" default answer "%s" buttons {"Cancel", "OK"} default button "OK" with title "AWTRIX Settings"`,
		escapeAppleScript(prompt), escapeAppleScript(def))
	return runOsascriptText(script)
}

// runHiddenDialog shows a hidden-answer dialog with no default value, so
// secrets never enter osascript's argv. Returns (value, true) on OK.
func runHiddenDialog(prompt string) (string, bool) {
	script := fmt.Sprintf(
		`set r to display dialog "%s" default answer "" with hidden answer buttons {"Cancel", "OK"} default button "OK" with title "AWTRIX Settings"`,
		escapeAppleScript(prompt))
	return runOsascriptText(script)
}

func runOsascriptText(dialogScript string) (string, bool) {
	out, err := exec.Command("osascript", "-e", dialogScript, "-e", "return text returned of r").Output()
	if err != nil {
		return "", false // Cancel exits non-zero (-128); treat any error as "no value"
	}
	return strings.TrimRight(string(out), "\n"), true
}

func showError(msg string) {
	script := fmt.Sprintf(
		`display dialog "%s" buttons {"OK"} default button "OK" with icon stop with title "AWTRIX Settings"`,
		escapeAppleScript(msg))
	_ = exec.Command("osascript", "-e", script).Run()
}

// settingsMenu owns the Settings submenu items and the producer.env path.
type settingsMenu struct {
	envPath  string
	source   *systray.MenuItem
	server   *systray.MenuItem
	token    *systray.MenuItem
	color    *systray.MenuItem
	ctxTrack *systray.MenuItem
	window   *systray.MenuItem
}

// buildSettingsMenu populates parent with the settings items and launches
// a click-handler goroutine per item. Returns the handle for refresh().
func buildSettingsMenu(parent *systray.MenuItem, envPath string) *settingsMenu {
	s := &settingsMenu{
		envPath:  envPath,
		source:   parent.AddSubMenuItem("Source: …", "Edit STATUS_SOURCE"),
		server:   parent.AddSubMenuItem("Server URL: …", "Edit STATUS_SERVER_URL"),
		token:    parent.AddSubMenuItem("Token: …", "Edit STATUS_TOKEN"),
		color:    parent.AddSubMenuItem("Source color: …", "Edit STATUS_SOURCE_COLOR"),
		ctxTrack: parent.AddSubMenuItemCheckbox("Context % tracking", "Toggle STATUS_CONTEXT_PCT_ENABLED", true),
		window:   parent.AddSubMenuItem("Context window: …", "Edit STATUS_CONTEXT_WINDOW_TOKENS"),
	}
	go s.handleText(s.source, "STATUS_SOURCE", "Source identifier for this machine:")
	go s.handleText(s.server, "STATUS_SERVER_URL", "Server URL (e.g. http://localhost:8080):")
	go s.handleToken()
	go s.handleText(s.color, "STATUS_SOURCE_COLOR", "Source colour as #RRGGBB (blank = none):")
	go s.handleText(s.window, "STATUS_CONTEXT_WINDOW_TOKENS", "Context window in tokens (blank or 0 = model default):")
	go s.handleToggle()
	return s
}

func (s *settingsMenu) handleText(mi *systray.MenuItem, key, prompt string) {
	for range mi.ClickedCh {
		rec := s.readRec()
		val, ok := runInputDialog(prompt, rec.get(key))
		if !ok {
			continue
		}
		norm, err := validateSetting(key, val)
		if err != nil {
			showError(err.Error())
			continue
		}
		if err := s.write(key, norm); err != nil {
			showError("save failed: " + err.Error())
		}
	}
}

func (s *settingsMenu) handleToken() {
	for range s.token.ClickedCh {
		val, ok := runHiddenDialog("New token (blank = keep current):")
		if !ok {
			continue
		}
		if strings.TrimSpace(val) == "" {
			continue // keep current
		}
		norm, err := validateSetting("STATUS_TOKEN", val)
		if err != nil {
			showError(err.Error())
			continue
		}
		if err := s.write("STATUS_TOKEN", norm); err != nil {
			showError("save failed: " + err.Error())
		}
	}
}

func (s *settingsMenu) handleToggle() {
	for range s.ctxTrack.ClickedCh {
		rec := s.readRec()
		next := "true"
		if isEnvTrue(rec.get("STATUS_CONTEXT_PCT_ENABLED")) {
			next = "false"
		}
		if err := s.write("STATUS_CONTEXT_PCT_ENABLED", next); err != nil {
			showError("save failed: " + err.Error())
			continue
		}
		if next == "true" {
			s.ctxTrack.Check()
		} else {
			s.ctxTrack.Uncheck()
		}
	}
}

func (s *settingsMenu) readRec() *envRec {
	rec, _ := readEnv(s.envPath)
	if rec == nil {
		rec = &envRec{}
	}
	return rec
}

func (s *settingsMenu) write(key, value string) error {
	rec, err := readEnv(s.envPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if rec == nil {
		rec = &envRec{}
	}
	rec.set(key, value)
	return writeEnvAtomic(s.envPath, rec.serialize())
}

// refresh updates the submenu titles + checkbox from the current env.
// Called from updateMenu on each poll.
func (s *settingsMenu) refresh(rec *envRec) {
	s.source.SetTitle("Source: " + orDash(rec.get("STATUS_SOURCE")))
	s.server.SetTitle("Server URL: " + orDash(rec.get("STATUS_SERVER_URL")))
	if rec.get("STATUS_TOKEN") != "" {
		s.token.SetTitle("Token: (set)")
	} else {
		s.token.SetTitle("Token: (unset)")
	}
	s.color.SetTitle("Source color: " + orDash(rec.get("STATUS_SOURCE_COLOR")))
	win := rec.get("STATUS_CONTEXT_WINDOW_TOKENS")
	if win == "" || win == "0" {
		win = "model default"
	}
	s.window.SetTitle("Context window: " + win)
	if isEnvTrue(rec.get("STATUS_CONTEXT_PCT_ENABLED")) {
		s.ctxTrack.Check()
	} else {
		s.ctxTrack.Uncheck()
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
