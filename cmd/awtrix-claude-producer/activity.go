package main

import (
	"encoding/json"
	"path/filepath"
)

// activityString composes a short "Tool: detail" display string from a hook's
// tool_name + tool_input. The whole composed string (prefix included) is
// truncated to 80. Unknown tools, missing fields, and malformed JSON all fall
// back to the bare tool name; an empty tool name yields "".
func activityString(toolName string, toolInput json.RawMessage) string {
	switch toolName {
	case "":
		return ""
	case "Bash":
		var ti struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(toolInput, &ti)
		if ti.Command != "" {
			return truncate("Bash: "+ti.Command, 80)
		}
		return "Bash"
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "Read":
		var ti struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		_ = json.Unmarshal(toolInput, &ti)
		p := ti.FilePath
		if p == "" {
			p = ti.NotebookPath
		}
		if p != "" {
			return truncate(toolName+": "+filepath.Base(p), 80)
		}
		return toolName
	case "Glob", "Grep":
		var ti struct {
			Pattern string `json:"pattern"`
		}
		_ = json.Unmarshal(toolInput, &ti)
		if ti.Pattern != "" {
			return truncate(toolName+": "+ti.Pattern, 80)
		}
		return toolName
	default:
		return toolName
	}
}
