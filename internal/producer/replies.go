package producer

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const RepliesPerRequest = 500

type Reply struct {
	At      int64  `json:"at"`
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
	Repo    string `json:"repo,omitempty"`
}

func (c *Client) Replies(ctx context.Context, replies []Reply) error {
	for len(replies) > 0 {
		n := min(len(replies), RepliesPerRequest)
		body, err := json.Marshal(struct {
			Replies []Reply `json:"replies"`
		}{replies[:n]})
		if err != nil {
			return err
		}
		if err := c.send(ctx, http.MethodPost, "/v1/day/replies", body); err != nil {
			return err
		}
		replies = replies[n:]
	}
	return nil
}

var automatedPromptPrefixes = []string{
	"<task-notification>", "<task>", "<local-command", "<system-reminder>", "<bash-stdout>", "<bash-stderr>",
}

func AutomatedPrompt(text string) bool {
	text = strings.TrimLeft(text, " \t\r\n")
	for _, p := range automatedPromptPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

const repoGitTimeout = 500 * time.Millisecond

var (
	repoCache  sync.Map
	repoGitDir = gitCommonDir
)

func RepoFromCwd(cwd string) string {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "." || cwd == "/" {
		return ""
	}
	if v, ok := repoCache.Load(cwd); ok {
		return v.(string)
	}
	repo := ""
	if dir := repoGitDir(cwd); dir != "" {
		repo = repoFromCommonDir(dir)
	}
	if repo == "" {
		repo = repoFromPath(cwd)
	}
	repo = Truncate(repo, ProjectMaxRunes)
	repoCache.Store(cwd, repo)
	return repo
}

func gitCommonDir(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), repoGitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func repoFromCommonDir(dir string) string {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) == ".git" {
		dir = filepath.Dir(dir)
	}
	name := strings.TrimSuffix(filepath.Base(dir), ".git")
	if name == "" || name == "." || name == "/" {
		return ""
	}
	return name
}

func repoFromPath(cwd string) string {
	parts := strings.Split(filepath.ToSlash(cwd), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		switch {
		case parts[i] == ".worktrees", i > 0 && parts[i] == "worktrees" && parts[i-1] == ".t3":
			if parts[i+1] != "" {
				return parts[i+1]
			}
		case parts[i] == ".claude" && parts[i+1] == "worktrees" && i > 0:
			return parts[i-1]
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if name, _, ok := strings.Cut(parts[i]+"-", "-wt-"); ok && name != "" {
			return name
		}
	}
	return ProjectFromCwd(cwd)
}
