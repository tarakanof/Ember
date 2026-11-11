package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarakanof/ember/internal/producer"
)

const dataDirEnv = "EMBER_DATA_DIR"

type serverEnv struct {
	User        bool
	DataDir     string
	DataDirFrom string
	EnvFile     string
	EnvFileFrom string
	LogFile     string
}

func (e serverEnv) active() bool {
	return e.User || e.DataDir != "" || e.EnvFile != ""
}

type serverEnvInput struct {
	user    bool
	envFile string
	home    string
	goos    string
	mkdir   bool
}

func userEnvFile(home string) string {
	return filepath.Join(home, ".config", "ember", "server.env")
}

func userDataDir(goos, home string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Ember")
	}
	return filepath.Join(home, ".local", "share", "ember")
}

func userLogFile(goos, home, dataDir string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Logs", "Ember", "server.log")
	}
	return filepath.Join(dataDir, "server.log")
}

func loadEnvFileInto(path string) (applied map[string]bool, err error) {
	vars, err := producer.ReadEnvFile(path)
	if err != nil {
		return nil, fmt.Errorf("env file: %w", err)
	}
	applied = map[string]bool{}
	for k, v := range vars {
		if _, present := os.LookupEnv(k); present {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return nil, fmt.Errorf("env file %s: %w", path, err)
		}
		applied[k] = true
	}
	return applied, nil
}

func setupServerEnv(in serverEnvInput) (serverEnv, error) {
	e := serverEnv{User: in.user}
	switch {
	case in.envFile != "":
		e.EnvFile, e.EnvFileFrom = in.envFile, "flag"
	case in.user:
		e.EnvFile, e.EnvFileFrom = userEnvFile(in.home), "-user"
	}
	var applied map[string]bool
	if e.EnvFile != "" {
		var err error
		if applied, err = loadEnvFileInto(e.EnvFile); err != nil {
			return e, err
		}
	}
	switch {
	case os.Getenv(dataDirEnv) != "":
		e.DataDir, e.DataDirFrom = os.Getenv(dataDirEnv), "env"
		if applied[dataDirEnv] {
			e.DataDirFrom = "env-file"
		}
	case in.user:
		e.DataDir, e.DataDirFrom = userDataDir(in.goos, in.home), "-user"
		if err := os.Setenv(dataDirEnv, e.DataDir); err != nil {
			return e, err
		}
	}
	if e.DataDir != "" && in.mkdir {
		if err := os.MkdirAll(e.DataDir, 0o700); err != nil {
			return e, fmt.Errorf("create data dir: %w", err)
		}
	}
	if in.user {
		e.LogFile = userLogFile(in.goos, in.home, e.DataDir)
	}
	return e, nil
}

func openServerLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	producer.RotateLogIfLarge(path, producer.DefaultLogThreshold)
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func startServerLog(path string) error {
	f, err := openServerLog(path)
	if err != nil {
		return err
	}
	producer.RedirectStandardIO(f)
	return nil
}

func (e serverEnv) describe() []string {
	var out []string
	if e.DataDir != "" {
		out = append(out, fmt.Sprintf("data_dir=%s (from %s)", e.DataDir, e.DataDirFrom))
	}
	if e.EnvFile != "" {
		out = append(out, fmt.Sprintf("env_file=%s (from %s)", e.EnvFile, e.EnvFileFrom))
	}
	if e.LogFile != "" {
		out = append(out, fmt.Sprintf("log_file=%s (from -user)", e.LogFile))
	}
	return out
}

func defaultDBPath() string {
	if dir := os.Getenv(dataDirEnv); dir != "" {
		return filepath.Join(dir, "pomodoro.db")
	}
	return "/var/lib/ember/pomodoro.db"
}
