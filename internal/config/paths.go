// Package config owns Config, paths, and secret env var names.
// AgentDir / WorkspaceDir are set once via SetDir at startup.
package config

import (
	"os"
	"path/filepath"
)

const (
	defaultAgentDir = "~/.velocity"
	configFilename  = "config.yaml"
	pidFilename     = "daemon.pid"
	logFilename     = "daemon.log"
	workspaceSubdir = "workspace"
)

var (
	AgentDir     string
	WorkspaceDir string
)

func init() {
	SetDir(defaultAgentDir)
}

// SetDir points velocity at a new data directory and reloads the config.
func SetDir(path string) {
	AgentDir = expandHome(path)
	WorkspaceDir = filepath.Join(AgentDir, workspaceSubdir)

	loadConfig()
}

func ConfigPath() string  { return filepath.Join(AgentDir, configFilename) }
func PidfilePath() string { return filepath.Join(AgentDir, pidFilename) }
func LogfilePath() string { return filepath.Join(AgentDir, logFilename) }

func WorkspacePath(jiraKey string) string {
	return filepath.Join(WorkspaceDir, jiraKey)
}

// EnsureRuntimeDirs creates AgentDir and WorkspaceDir.
func EnsureRuntimeDirs() error {
	for _, d := range []string{AgentDir, WorkspaceDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func EnsureDir() error {
	return os.MkdirAll(AgentDir, 0o755)
}

func expandHome(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
