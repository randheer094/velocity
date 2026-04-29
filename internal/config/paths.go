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
	resourcesSubdir = "resources"
	versionFilename = "VERSION"
)

var (
	AgentDir     string
	WorkspaceDir string
)

func init() {
	if err := SetDir(defaultAgentDir); err != nil {
		// init runs before any caller can recover; fall back to a
		// safe-but-unusable empty path so a missing $HOME doesn't
		// crash the binary on import. Subcommands that actually need
		// the dir will fail loudly via requireConfig.
		AgentDir = ""
		WorkspaceDir = ""
	}
}

// SetDir points velocity at a new data directory and reloads the
// config. Returns an error if the path can't be resolved (e.g. "~"
// expansion fails because $HOME is unset).
func SetDir(path string) error {
	expanded, err := expandHome(path)
	if err != nil {
		return err
	}
	AgentDir = expanded
	WorkspaceDir = filepath.Join(AgentDir, workspaceSubdir)

	loadConfig()
	return nil
}

func ConfigPath() string  { return filepath.Join(AgentDir, configFilename) }
func PidfilePath() string { return filepath.Join(AgentDir, pidFilename) }
func LogfilePath() string { return filepath.Join(AgentDir, logFilename) }

func WorkspacePath(jiraKey string) string {
	return filepath.Join(WorkspaceDir, jiraKey)
}

// ResourcesDir is the local cache populated by `velocity setup` and
// refreshed by `velocity update-prompts`. The daemon reads prompts and
// project templates from here at runtime.
func ResourcesDir() string {
	return filepath.Join(AgentDir, resourcesSubdir)
}

// ResourcesVersionPath is the resolved path of the VERSION file under
// the resources cache.
func ResourcesVersionPath() string {
	return filepath.Join(ResourcesDir(), versionFilename)
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

func expandHome(path string) (string, error) {
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}
