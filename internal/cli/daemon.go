package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/pidfile"
	"github.com/randheer094/velocity/internal/prompts"
	"github.com/randheer094/velocity/internal/server"
)

const daemonEnvMarker = "VELOCITY_DAEMON_CHILD"

func requireConfig() error {
	if config.Get() != nil {
		return nil
	}
	if e := config.LoadError(); e != "" {
		return fmt.Errorf("%s\nFix %s (see config.example.yaml)", e, config.ConfigPath())
	}
	return fmt.Errorf("velocity is not configured: write %s (see config.example.yaml)", config.ConfigPath())
}

// validateResources checks that the resources cache exists and that
// `resources.repo_slug` / `resources.version` are set in config.yaml.
// It does NOT call prompts.Load — the parent of a `velocity start`
// invocation only needs the fast structural check; the daemon child
// re-runs the gate via requireResources and actually loads.
//
// Callers must invoke requireConfig first.
func validateResources() error {
	cfg := config.Get()
	if cfg.Resources.RepoSlug == "" || cfg.Resources.Version == "" {
		return errors.New("resources not configured; run `velocity setup` first")
	}
	if _, err := os.Stat(config.ResourcesDir()); err != nil {
		return fmt.Errorf("%w; run `velocity setup` first", err)
	}
	return nil
}

// requireResources extends validateResources with a full prompts.Load.
// Used on the daemon hot path so the manifest + every template is
// parsed before the HTTP listener comes up; failures abort start with
// a hint to re-run `velocity setup`.
//
// Callers must invoke requireConfig first.
func requireResources() error {
	if err := validateResources(); err != nil {
		return err
	}
	if err := prompts.Load(config.ResourcesDir()); err != nil {
		return fmt.Errorf("%w; resources missing or stale, run `velocity setup` first", err)
	}
	return nil
}

func newStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Run the webhook server (detached by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireConfig(); err != nil {
				return err
			}
			isServerProcess := foreground || os.Getenv(daemonEnvMarker) == "1"
			if isServerProcess {
				if err := requireResources(); err != nil {
					return err
				}
				return server.Run()
			}
			// Parent of a detach: only structural validation. The
			// child process loads prompts itself, so loading them
			// here would just throw the result away.
			if err := validateResources(); err != nil {
				return err
			}
			return detach()
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in the foreground")
	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon (SIGTERM, SIGKILL after 10s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := pidfile.Read(config.PidfilePath())
			if err != nil {
				return err
			}
			if entry.PID == 0 || !pidfile.VerifyAlive(entry) {
				_ = pidfile.Remove(config.PidfilePath())
				fmt.Println("stopped")
				return nil
			}
			return stop(entry.PID)
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop then start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireConfig(); err != nil {
				return err
			}
			if err := validateResources(); err != nil {
				return err
			}
			if entry, _ := pidfile.Read(config.PidfilePath()); entry.PID != 0 && pidfile.VerifyAlive(entry) {
				if err := stop(entry.PID); err != nil {
					return err
				}
			} else {
				_ = pidfile.Remove(config.PidfilePath())
			}
			return detach()
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print running/stopped and exit 0 if running",
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, _ := pidfile.Read(config.PidfilePath())
			if entry.PID == 0 || !pidfile.VerifyAlive(entry) {
				_ = pidfile.Remove(config.PidfilePath())
				fmt.Println("stopped")
				os.Exit(1)
				return nil
			}
			fmt.Printf("running (pid %d)\n", entry.PID)
			return nil
		},
	}
}

func writePid(pid int) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		// Fall back to a legacy-format pidfile so stop/status still
		// work; the exe-fingerprint check in VerifyAlive becomes a
		// no-op for this entry.
		exe = ""
	}
	return pidfile.Write(config.PidfilePath(), pidfile.Entry{PID: pid, ExePath: exe})
}

// readPid is a thin shim around pidfile.Read kept for the older
// internal call sites (and tests that pre-date the pidfile package).
// New code should prefer pidfile.Read directly so it can also reach
// the ExePath field for VerifyAlive.
func readPid() (int, error) {
	entry, err := pidfile.Read(config.PidfilePath())
	if err != nil {
		return 0, err
	}
	return entry.PID, nil
}

func stop(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = pidfile.Remove(config.PidfilePath())
			fmt.Println("stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = pidfile.Remove(config.PidfilePath())
	fmt.Println("stopped (SIGKILL)")
	return nil
}

func detach() error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logfile, err := os.OpenFile(config.LogfilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logfile.Close()

	cmd := exec.Command(exe, "--dir", config.AgentDir, "start", "--foreground")
	cmd.Env = append(os.Environ(), daemonEnvMarker+"=1")
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	if err := writePid(pid); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	fmt.Printf("started (pid %d)\n", pid)
	return nil
}
