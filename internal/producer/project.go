package producer

import (
	"path/filepath"
	"strings"
)

const ProjectMaxRunes = 32

func ProjectFromCwd(cwd string) string {
	cwd = strings.TrimRight(strings.TrimSpace(cwd), "/\\")
	if cwd == "" {
		return ""
	}
	base := filepath.Base(cwd)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return Truncate(base, ProjectMaxRunes)
}

func ProjectFromTitle(title string) string {
	return Truncate(strings.Join(strings.Fields(title), " "), ProjectMaxRunes)
}

func (c Common) Project(name string) string {
	if !c.SendProject {
		return ""
	}
	return name
}
