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

func startPromptReply(ctx context.Context, cfg Config, client *Client, sessionID string, in hookInput) <-chan struct{} {
	done := make(chan struct{})
	if !humanEntrypoint(promptEnv) || producer.AutomatedPrompt(in.Prompt) {
		close(done)
		return done
	}
	at := hookNow().Unix()
	go func() {
		defer close(done)
		r := producer.Reply{At: at, Source: cfg.Source, Tool: "claude", Session: sessionID,
			Repo: cfg.Project(producer.RepoFromCwd(in.CWD))}
		_ = client.Replies(ctx, []producer.Reply{r})
	}()
	return done
}
