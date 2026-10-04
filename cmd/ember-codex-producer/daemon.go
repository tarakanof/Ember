package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const httpTimeout = 5 * time.Second

var daemonFailLog = producer.NewFailureLogger(time.Minute)

func runDaemon() {
	rotateCodexLogs()
	openDaemonLog("ember-codex-producer")
	cfg, err := loadConfig()
	if err != nil || cfg.Source == "" || (cfg.ServerURL == "" && !cfg.ServerAuto) {
		fmt.Fprintln(os.Stderr, "codex producer: EMBER_SOURCE/EMBER_SERVER_URL not set; nothing to do")
		os.Exit(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	auto, ok := producer.DaemonServer(ctx, home, cfg.ServerURL, cfg.ServerAuto, cfg.ServerInstance)
	if !ok {
		return
	}
	w := newWatcher(cfg)
	var as *appServer
	if cfg.AppServerEnabled {
		as = newAppServer(cfg)
		go as.run(ctx)
	}
	client := producer.NewClient(cfg.ServerURL, cfg.Token, httpTimeout).WithAutoServer(auto)
	if path, err := producer.LinkStatusPath("codex-producer"); err == nil {
		client.WithLinkStatus(producer.NewLinkStatus(path))
	}

	ticker := time.NewTicker(time.Duration(cfg.PollIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		runOnce(ctx, w, as, client)
		select {
		case <-ctx.Done():
			for _, ss := range w.sessions {
				_ = removeMarker(cfg.StateDir, ss.uuid)
			}
			if as != nil {
				for id := range as.tick().owned {
					_ = removeMarker(cfg.StateDir, id)
				}
			}
			return
		case <-ticker.C:
		}
	}
}

func openDaemonLog(name string) {
	f, err := producer.OpenDaemonLog(name)
	if err != nil {
		return
	}
	producer.RedirectStandardIO(f)
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
}

// cycle merges one poll of the app-server source (nil when disabled) and the
// rollout watcher. The app-server wins for the threads it sees; on a
// disconnect its sessions go back to the watcher, or get a DELETE when the
// watcher has no live rollout for them.
func cycle(w *watcher, as *appServer) (posts []producer.StatusRequest, deletes []producer.DeleteRequest, usages []producer.UsageRequest) {
	var ap apTick
	if as != nil {
		ap = as.tick()
	}
	w.owned, w.rateExtra = ap.owned, ap.rate
	posts, deletes, usages = w.tick()
	posts = append(ap.posts, posts...)
	deletes = append(deletes, ap.deletes...)
	for _, id := range ap.released {
		if !w.posted(id) {
			deletes = append(deletes, producer.DeleteRequest{Source: w.cfg.Source, Tool: "codex", Session: id})
		}
	}
	return posts, deletes, usages
}

func runOnce(ctx context.Context, w *watcher, as *appServer, client *producer.Client) {
	posts, deletes, usages := cycle(w, as)
	for _, req := range posts {
		if err := client.Post(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_post", "status POST failed", "err", err)
		}
		if body, err := json.Marshal(req); err == nil {
			_ = writeMarker(w.cfg.StateDir, req.Session, body)
		}
	}
	for _, req := range deletes {
		if err := client.Delete(ctx, req); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_delete", "status DELETE failed", "err", err)
		}
		_ = removeMarker(w.cfg.StateDir, req.Session)
	}
	for _, u := range usages {
		if err := client.Usage(ctx, u); err != nil {
			daemonFailLog.Warn(slog.Default(), "codex_usage", "usage POST failed", "err", err)
		}
	}
}

func rotateCodexLogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	producer.RotateLogIfLarge(producer.LogPath(home, "ember-codex-producer"), producer.DefaultLogThreshold)
}
