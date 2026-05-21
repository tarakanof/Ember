package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type envRec struct {
	lines []envLine
}

type envLine struct {
	raw   string // verbatim original line (or new "K=V")
	key   string // empty if not a key=value line
	value string
}

func parseEnv(r io.Reader) (*envRec, error) {
	rec := &envRec{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			rec.lines = append(rec.lines, envLine{raw: line})
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			rec.lines = append(rec.lines, envLine{raw: line})
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip simple wrapping quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		rec.lines = append(rec.lines, envLine{raw: line, key: key, value: val})
	}
	return rec, s.Err()
}

func readEnv(path string) (*envRec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseEnv(f)
}

// get returns the last occurrence of key, matching the producer's parser
// semantics (cmd/awtrix-claude-producer/config.go uses map[string]string with
// last-write-wins). Aligns the menu's view with the value the producer
// actually reads when producer.env contains duplicate keys.
func (r *envRec) get(key string) string {
	last := ""
	for _, l := range r.lines {
		if l.key == key {
			last = l.value
		}
	}
	return last
}

// set updates the last occurrence of key (so a write through the prefs form
// changes the value the producer will read). Earlier duplicate lines are left
// untouched; the user is responsible for cleaning those up if they care.
func (r *envRec) set(key, value string) {
	lastIdx := -1
	for i := range r.lines {
		if r.lines[i].key == key {
			lastIdx = i
		}
	}
	if lastIdx >= 0 {
		r.lines[lastIdx].value = value
		r.lines[lastIdx].raw = key + "=" + value
		return
	}
	r.lines = append(r.lines, envLine{raw: key + "=" + value, key: key, value: value})
}

func (r *envRec) serialize() string {
	var b strings.Builder
	for _, l := range r.lines {
		b.WriteString(l.raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// ctrlChars is the set of ASCII control characters (0x00–0x1f plus DEL)
// rejected in settings values.
var ctrlChars = func() string {
	var s []byte
	for i := 0; i < 32; i++ {
		s = append(s, byte(i))
	}
	s = append(s, 127)
	return string(s)
}()

// writeEnvAtomic writes content to path with mode 0600 via temp file +
// rename. It refuses to write when the containing directory is wider
// than 0700 (prevents same-laptop non-owner reads of a token-bearing
// file), and hard-fails if chmod fails.
func writeEnvAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err == nil && info.IsDir() && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("config dir %s is wider than 0700; run `chmod 0700 %s` and try again", dir, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*.env")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("chmod 0600 failed: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
