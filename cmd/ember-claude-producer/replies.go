package main

import (
	"context"
	"os"

	"github.com/tarakanof/ember/internal/producer"
)

var promptEnv = os.Getenv

func humanEntrypoint(getenv func(string) string) bool {
	switch getenv("CLAUDE_CODE_ENTRYPOINT") {
	case "cli":
		return getenv("CLAUDE_AGENT_SDK_VERSION") == ""
	case "claude-desktop":
		return true
	}
	return false
}

func postPromptReply(ctx context.Context, cfg Config, client *Client, sessionID string, in hookInput) {
	if !humanEntrypoint(promptEnv) || producer.AutomatedPrompt(in.Prompt) {
		return
	}
	r := producer.Reply{At: hookNow().Unix(), Source: cfg.Source, Tool: "claude", Session: sessionID,
		Repo: cfg.Project(producer.RepoFromCwd(in.CWD))}
	_ = client.Replies(ctx, []producer.Reply{r})
}
