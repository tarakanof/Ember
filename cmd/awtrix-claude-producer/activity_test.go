package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityString(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"bash", "Bash", `{"command":"npm run test"}`, "Bash: npm run test"},
		{"edit", "Edit", `{"file_path":"/repo/cmd/render.go"}`, "Edit: render.go"},
		{"write", "Write", `{"file_path":"/a/b/main.go"}`, "Write: main.go"},
		{"read", "Read", `{"file_path":"/x/y/notes.md"}`, "Read: notes.md"},
		{"multiedit", "MultiEdit", `{"file_path":"/p/q.go"}`, "MultiEdit: q.go"},
		{"notebook", "NotebookEdit", `{"notebook_path":"/n/analysis.ipynb"}`, "NotebookEdit: analysis.ipynb"},
		{"grep", "Grep", `{"pattern":"TODO"}`, "Grep: TODO"},
		{"glob", "Glob", `{"pattern":"**/*.go"}`, "Glob: **/*.go"},
		{"unknown tool", "WebFetch", `{"url":"http://x"}`, "WebFetch"},
		{"missing field", "Bash", `{}`, "Bash"},
		{"malformed json", "Bash", `{not json`, "Bash"},
		{"empty tool", "", `{}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := activityString(tc.tool, json.RawMessage(tc.input))
			if got != tc.want {
				t.Errorf("activityString(%q, %s) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

func TestActivityStringTruncatesComposedString(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := activityString("Bash", json.RawMessage(`{"command":"`+long+`"}`))
	if len(got) > 80 {
		t.Errorf("activityString result = %d chars, want <= 80", len(got))
	}
	if !strings.HasPrefix(got, "Bash: aaa") {
		t.Errorf("prefix lost during truncation: %q", got)
	}
}
