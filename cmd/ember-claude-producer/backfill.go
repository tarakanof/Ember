package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const (
	backfillDefaultDays = 30
	backfillTimeout     = 5 * time.Minute
	backfillHTTPTimeout = 30 * time.Second
)

type transcriptLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Entrypoint  string          `json:"entrypoint"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	Origin      json.RawMessage `json:"origin"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func runBackfill(args []string) {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	days := fs.Int("days", backfillDefaultDays, "how many days of transcripts to read")
	dryRun := fs.Bool("dry-run", false, "count the prompts without posting them")
	_ = fs.Parse(args)
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || cfg.ServerURL == "" {
		fmt.Fprintln(os.Stderr, "backfill: EMBER_SOURCE/EMBER_SERVER_URL not set (run discover for an mDNS server)")
		os.Exit(1)
	}
	if *days < 1 {
		fmt.Fprintln(os.Stderr, "backfill: --days must be at least 1")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
	defer cancel()
	var client *Client
	if !*dryRun {
		client = producer.NewClient(cfg.ServerURL, cfg.Token, backfillHTTPTimeout)
	}
	since := time.Now().AddDate(0, 0, -*days)
	if err := backfill(ctx, cfg, claudeProjectsDir(), since, client, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "backfill:", err)
		os.Exit(1)
	}
}

func claudeProjectsDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func backfill(ctx context.Context, cfg Config, dir string, since time.Time, client *Client, out io.Writer) error {
	replies, files, err := scanTranscripts(dir, since, cfg)
	if err != nil {
		return err
	}
	if client == nil {
		fmt.Fprintf(out, "backfill: %d prompts in %d transcripts since %s (dry run, nothing posted)\n", len(replies), files, since.Format("2006-01-02"))
		return nil
	}
	if err := client.Replies(ctx, replies); err != nil {
		var se *producer.ServerError
		if errors.As(err, &se) && se.Code == 404 {
			return fmt.Errorf("the server has no day clock replies (update it, or turn day.enabled on): %w", err)
		}
		return err
	}
	fmt.Fprintf(out, "backfill: posted %d prompts from %d transcripts since %s\n", len(replies), files, since.Format("2006-01-02"))
	return nil
}

func scanTranscripts(dir string, since time.Time, cfg Config) ([]producer.Reply, int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return nil, 0, err
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.ModTime().Before(since) {
			files = append(files, file{p, info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	seen := map[string]bool{}
	var replies []producer.Reply
	for _, f := range files {
		fh, err := os.Open(f.path)
		if err != nil {
			continue
		}
		r := bufio.NewReaderSize(fh, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if rep, ok := transcriptPrompt(line, since, cfg); ok && !seen[rep.key] {
				seen[rep.key] = true
				replies = append(replies, rep.Reply)
			}
			if err != nil {
				break
			}
		}
		fh.Close()
	}
	return replies, len(files), nil
}

type keyedReply struct {
	producer.Reply
	key string
}

var userTypeMarker = []byte(`"type":"user"`)

func transcriptPrompt(line []byte, since time.Time, cfg Config) (keyedReply, bool) {
	if !bytes.Contains(line, userTypeMarker) {
		return keyedReply{}, false
	}
	var l transcriptLine
	if json.Unmarshal(line, &l) != nil || l.Type != "user" || l.IsSidechain || l.IsMeta {
		return keyedReply{}, false
	}
	if l.Entrypoint != "cli" && l.Entrypoint != "claude-desktop" {
		return keyedReply{}, false
	}
	if kind := originKind(l.Origin); kind != "" && kind != "human" {
		return keyedReply{}, false
	}
	text, ok := promptText(l.Message.Content)
	if !ok || producer.AutomatedPrompt(text) {
		return keyedReply{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil || at.Before(since) {
		return keyedReply{}, false
	}
	session := sanitizeSessionID(l.SessionID, l.Cwd)
	key := l.UUID
	if key == "" {
		key = session + "\x00" + l.Timestamp
	}
	return keyedReply{Reply: producer.Reply{At: at.Unix(), Source: cfg.Source, Tool: "claude", Session: session,
		Repo: cfg.Project(producer.RepoFromCwd(l.Cwd))}, key: key}, true
}

func originKind(raw json.RawMessage) string {
	var o struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return ""
	}
	return o.Kind
}

func promptText(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return "", false
	}
	text := ""
	for _, p := range parts {
		switch p.Type {
		case "tool_result":
			return "", false
		case "text":
			if text == "" {
				text = p.Text
			}
		}
	}
	return text, true
}
