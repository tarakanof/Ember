package producer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestAutomatedPrompt(t *testing.T) {
	for text, want := range map[string]bool{
		"fix the build":                       false,
		"<command-name>/clear</command-name>": false,
		"<bash-input>ls</bash-input>":         false,
		"<task-notification>done":             true,
		" <task>\nreview":                     true,
		"<local-command-stdout>x":             true,
		"<system-reminder>x":                  true,
		"<bash-stdout>x":                      true,
		"<bash-stderr>x":                      true,
	} {
		if got := AutomatedPrompt(text); got != want {
			t.Errorf("AutomatedPrompt(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestRepoFromPathRules(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = os.UserHomeDir })
	for cwd, want := range map[string]string{
		"/Users/dt/.t3/worktrees/Ember/feat-730-x":           "Ember",
		"/Users/dt/.t3/worktrees/Ember/feat-730-x/cmd/ember": "Ember",
		"/Users/dt/Github/cinder/.claude/worktrees/agent-1":  "cinder",
		"/Users/dt/Github/.worktrees/Lutaro/fix-1":           "Lutaro",
		"/Users/dt/Github/Ember-wt/day-replies":              "Ember",
		"/Users/dt/Github/Amadeus":                           "Amadeus",
		home:                                                 "",
	} {
		if got := repoFromPath(cwd); got != want {
			t.Errorf("repoFromPath(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestRepoFromCwdUsesTheMainCheckoutOfAWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	main := filepath.Join(root, "Widget")
	wt := filepath.Join(root, "elsewhere", "feature-branch")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q")
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	git(main, "worktree", "add", "-q", wt)
	if got := RepoFromCwd(wt); got != "Widget" {
		t.Fatalf("RepoFromCwd(worktree) = %q, want Widget", got)
	}
	if got := RepoFromCwd(filepath.Join(root, "elsewhere")); got != "elsewhere" {
		t.Fatalf("RepoFromCwd(non-repo) = %q, want the folder name", got)
	}
}

func TestRepoFromCwdIsCachedPerCwd(t *testing.T) {
	calls := 0
	repoGitDir = func(string) string { calls++; return "/src/Gadget/.git" }
	t.Cleanup(func() { repoGitDir = gitCommonDir; repoCache = sync.Map{} })
	for range 3 {
		if got := RepoFromCwd("/tmp/cached-cwd-730"); got != "Gadget" {
			t.Fatalf("RepoFromCwd = %q, want Gadget", got)
		}
	}
	if calls != 1 {
		t.Fatalf("git ran %d times, want 1", calls)
	}
}

func TestClientRepliesChunksAndCarriesNoText(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/day/replies" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Replies []map[string]any `json:"replies"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatal(err)
		}
		for _, rep := range body.Replies {
			for k := range rep {
				switch k {
				case "at", "source", "tool", "session", "repo":
				default:
					t.Errorf("unexpected field %q", k)
				}
			}
		}
		sizes = append(sizes, len(body.Replies))
	}))
	defer srv.Close()
	rs := make([]Reply, RepliesPerRequest+3)
	for i := range rs {
		rs[i] = Reply{At: int64(i + 1), Source: "m4", Tool: "claude", Session: "s"}
	}
	if err := NewClient(srv.URL, "tok", 0).Replies(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0] != RepliesPerRequest || sizes[1] != 3 {
		t.Fatalf("batches = %v, want [500 3]", sizes)
	}
}
