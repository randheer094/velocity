package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/server"
)

const daemonEnvMarker = "VELOCITY_DAEMON_CHILD"

func requireConfig() error {
	if config.Get() != nil {
		return nil
	}
	if e := config.LoadError(); e != "" {
		return fmt.Errorf("%s\nFix the config or re-run `velocity setup --edit`", e)
	}
	return errors.New("velocity is not configured. Run `velocity setup` first")
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
			if foreground || os.Getenv(daemonEnvMarker) == "1" {
				return server.Run()
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
			pid, err := readPid()
			if err != nil {
				return err
			}
			if pid == 0 {
				fmt.Println("stopped")
				return nil
			}
			return stop(pid)
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
			if pid, _ := readPid(); pid != 0 {
				if err := stop(pid); err != nil {
					return err
				}
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
			pid, _ := readPid()
			if pid == 0 {
				fmt.Println("stopped")
				os.Exit(1)
				return nil
			}
			if err := syscall.Kill(pid, 0); err != nil {
				fmt.Println("stopped")
				_ = os.Remove(config.PidfilePath())
				os.Exit(1)
				return nil
			}
			fmt.Printf("running (pid %d)\n", pid)
			return nil
		},
	}
}

func readPid() (int, error) {
	data, err := os.ReadFile(config.PidfilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pidfile: %w", err)
	}
	return pid, nil
}

func writePid(pid int) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(config.PidfilePath(), []byte(strconv.Itoa(pid)), 0o644)
}

func stop(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = os.Remove(config.PidfilePath())
			fmt.Println("stopped")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(config.PidfilePath())
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
	logfile, err := os.OpenFile(config.LogfilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
	if err := writePid(cmd.Process.Pid); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	fmt.Printf("started (pid %d)\n", cmd.Process.Pid)
	return nil
}
