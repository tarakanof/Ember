package main

import (
	"bufio"
	"io"
	"os"
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

func (r *envRec) get(key string) string {
	for _, l := range r.lines {
		if l.key == key {
			return l.value
		}
	}
	return ""
}

func (r *envRec) set(key, value string) {
	for i := range r.lines {
		if r.lines[i].key == key {
			r.lines[i].value = value
			r.lines[i].raw = key + "=" + value
			return
		}
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
