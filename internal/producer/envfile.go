package producer

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ReadEnvFile reads a KEY=value file, requiring it to be a regular file with
// mode 0600 owned by the current user (it carries a bearer token). A missing
// file yields an empty map and no error. Quoted values are unquoted; comment
// and blank lines are skipped.
func ReadEnvFile(path string) (map[string]string, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if stat.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%s permission must be 0600 (got %#o)", path, stat.Mode().Perm())
	}
	if sysStat, ok := stat.Sys().(*syscall.Stat_t); ok {
		if int(sysStat.Uid) != os.Geteuid() {
			return nil, fmt.Errorf("%s must be owned by current user", path)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out, s.Err()
}
