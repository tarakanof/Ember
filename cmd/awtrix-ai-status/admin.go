package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
)

// versionInfo is the JSON body served by /version. Computed once at startup.
type versionInfo struct {
	Binary    string `json:"binary"`
	Revision  string `json:"revision"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
}

func computeVersionInfo() versionInfo {
	info := versionInfo{Binary: "awtrix-ai-status", GoVersion: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Revision = s.Value
			case "vcs.modified":
				info.Dirty = s.Value == "true"
			}
		}
	}
	return info
}

func handleVersion(info versionInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}
}
