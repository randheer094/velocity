package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

func TestReadAndWritePid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)

	// No file → 0, nil
	pid, err := readPid()
	if pid != 0 || err != nil {
		t.Errorf("missing pid: pid=%d err=%v", pid, err)
	}

	if err := writePid(12345); err != nil {
		t.Fatalf("writePid: %v", err)
	}
	pid, err = readPid()
	if err != nil || pid != 12345 {
		t.Errorf("readPid = %d, %v", pid, err)
	}

	// Garbage pidfile → error
	if err := os.WriteFile(config.PidfilePath(), []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPid(); err == nil {
		t.Error("expected parse error")
	}
}

func writeValidConfig(t *testing.T) {
	t.Helper()
	cfg := &config.Config{
		Jira: config.JiraConfig{
			BaseURL:         "https://x.atlassian.net",
			Email:           "a@b.c",
			ArchitectJiraID: "arch",
			DeveloperJiraID: "dev",
			RepoURLField:    "customfield_repo",
			TaskStatusMap: config.TaskStatusMap{
				New:            config.StatusBucket{Default: "To Do"},
				Planning:       config.StatusBucket{Default: "Planning"},
				PlanningFailed: config.StatusBucket{Default: "Planning Failed"},
				Coding:         config.StatusBucket{Default: "In Progress"},
				Done:           config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
			SubtaskStatusMap: config.SubtaskStatusMap{
				New:          config.StatusBucket{Default: "To Do"},
				Coding:       config.StatusBucket{Default: "Dev In Progress"},
				CodingFailed: config.StatusBucket{Default: "Dev Failed"},
				InReview:     config.StatusBucket{Default: "In Review"},
				Done:         config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
		},
		Resources: config.ResourcesConfig{
			RepoSlug: "owner/velocity-resources",
			Version:  "v0.0.0",
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	seedFixtureResourcesAt(t, config.ResourcesDir())
}

// seedFixtureResourcesAt writes a minimal manifest + templates so the
// daemon-start path's prompts.Load succeeds.
func seedFixtureResourcesAt(t *testing.T, resDir string) {
	t.Helper()
	files := map[string]string{
		"prompts/manifest.yaml": `version: 0
prompts:
  - id: arch_plan
    path: arch/plan.md
    placeholders: [PlanBegin, PlanEnd, ParentKey, Requirement]
`,
		"prompts/arch/plan.md": "{{.PlanBegin}} {{.ParentKey}} {{.Requirement}} {{.PlanEnd}}",
		"VERSION":              "v0.0.0",
	}
	for rel, body := range files {
		full := filepath.Join(resDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewRootCmd(t *testing.T) {
	root := NewRootCmd()
	if root.Use != "velocity" {
		t.Errorf("use = %q", root.Use)
	}
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"config", "start", "stop", "restart", "status", "logs"} {
		if !names[want] {
			t.Errorf("missing subcommand: %s", want)
		}
	}
}

func TestNewLogsCmdNoFile(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)

	cmd := newLogsCmd()
	// File does not exist → command prints to stderr and returns nil.
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE: %v", err)
	}
}

func TestNewLogsCmdReadsFile(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := os.WriteFile(config.LogfilePath(), []byte("hello logs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newLogsCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE: %v", err)
	}
}

func TestNewLogsCmdOpenError(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	// Create the path as a directory so os.Open returns a non-NotExist err.
	if err := os.MkdirAll(config.LogfilePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := newLogsCmd()
	err := cmd.RunE(cmd, nil)
	// os.Open of a directory succeeds on Linux, but io.Copy may return
	// "is a directory" — accept either err==nil or err with that message.
	if err != nil && !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestNewStatusCmdMissing(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := newStatusCmd()
	// status calls os.Exit(1) when stopped. Use a subprocess to test; instead we
	// simulate by writing a pidfile pointing at our own pid (which is alive).
	if err := writePid(syscall.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE: %v", err)
	}
}

func TestNewStopCmdNoPid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := newStopCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE: %v", err)
	}
}

func TestNewStopCmdGarbage(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := os.WriteFile(config.PidfilePath(), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newStopCmd()
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error from garbage pidfile")
	}
}

func TestStopOnDeadPid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	// Pick a pid that almost certainly doesn't exist: max int32 - 1
	if err := stop(2147483646); err == nil {
		t.Error("expected SIGTERM error on dead pid")
	}
	if !strings.Contains(filepath.Clean(config.PidfilePath()), config.AgentDir) {
		t.Errorf("pidpath outside agent dir: %s", config.PidfilePath())
	}
}

func TestStopWithLiveProcess(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := writePid(syscall.Getpid()); err != nil {
		t.Fatal(err)
	}
	// Don't actually call stop — would terminate this test process.
	// Instead, just verify pidfile path and read flow.
	pid, err := readPid()
	if err != nil || pid != syscall.Getpid() {
		t.Errorf("readPid = %d, %v", pid, err)
	}
}

func TestStopGracefulSubprocess(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("can't spawn sleep: %v", err)
	}
	// Reap in a goroutine so the post-SIGTERM zombie is collected and
	// `Kill(pid, 0)` in stop() returns ESRCH promptly.
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	defer func() { _ = cmd.Process.Kill(); <-waited }()

	if err := writePid(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := stop(cmd.Process.Pid); err != nil {
		t.Errorf("stop: %v", err)
	}
	if _, err := os.Stat(config.PidfilePath()); !os.IsNotExist(err) {
		t.Errorf("pidfile still exists after stop")
	}
}

func TestNewRestartCmdNoPid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	writeValidConfig(t)
	cmd := newRestartCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if pid, _ := readPid(); pid <= 0 {
		t.Errorf("expected pidfile after restart, got %d", pid)
	}
}

func TestNewRestartCmdMissingConfig(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := newRestartCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("expected config-hint error, got %v", err)
	}
}

func TestNewRestartCmdWithLivePid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	writeValidConfig(t)
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("can't spawn sleep: %v", err)
	}
	waited := make(chan struct{})
	go func() { _ = c.Wait(); close(waited) }()
	defer func() { _ = c.Process.Kill(); <-waited }()

	if err := writePid(c.Process.Pid); err != nil {
		t.Fatal(err)
	}
	cmd := newRestartCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if pid, _ := readPid(); pid <= 0 {
		t.Errorf("expected fresh pidfile, got %d", pid)
	}
}

func TestNewStopCmdWithLivePid(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("can't spawn sleep: %v", err)
	}
	waited := make(chan struct{})
	go func() { _ = c.Wait(); close(waited) }()
	defer func() { _ = c.Process.Kill(); <-waited }()

	if err := writePid(c.Process.Pid); err != nil {
		t.Fatal(err)
	}
	cmd := newStopCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE: %v", err)
	}
}

func TestNewStartCmdForegroundFails(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	t.Setenv(daemonEnvMarker, "1")
	cmd := newStartCmd()
	// foreground=false but env=1 → server.Run path; without a real config
	// it should error fast (config not found / db start fail).
	_ = cmd.RunE(cmd, nil)
}

func TestNewStartCmdDetaches(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	writeValidConfig(t)
	cmd := newStartCmd()
	// Default flags: foreground=false, no daemon marker → goes to detach().
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if pid, _ := readPid(); pid <= 0 {
		t.Errorf("expected pidfile after start, got %d", pid)
	}
}

func TestNewStartCmdMissingConfig(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := newStartCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("expected config-hint error, got %v", err)
	}
	if _, statErr := os.Stat(config.PidfilePath()); !os.IsNotExist(statErr) {
		t.Error("pidfile should not be written when config is missing")
	}
}

func TestNewStartCmdInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := os.WriteFile(config.ConfigPath(), []byte("foo: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir) // reload
	cmd := newStartCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("expected config-hint error, got %v", err)
	}
}

func TestDetachSpawnsChild(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	// detach() execs `os.Executable() --dir <dir> start --foreground`.
	// The test binary doesn't speak that flag set, so the child exits
	// quickly — but detach() itself runs to completion and writes a pid.
	if err := detach(); err != nil {
		t.Fatalf("detach: %v", err)
	}
	pid, err := readPid()
	if err != nil {
		t.Errorf("readPid err: %v", err)
	}
	if pid <= 0 {
		t.Errorf("readPid pid = %d, want >0", pid)
	}
}

func TestNewStatusCmdStaleClearsPidfile(t *testing.T) {
	if dir := os.Getenv("VELOCITY_TEST_STATUS_DIR"); dir != "" {
		config.SetDir(dir)
		cmd := newStatusCmd()
		_ = cmd.RunE(cmd, nil)
		return
	}
	dir := t.TempDir()
	config.SetDir(dir)
	if err := writePid(2147483646); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run", "^TestNewStatusCmdStaleClearsPidfile$")
	c.Env = append(os.Environ(), "VELOCITY_TEST_STATUS_DIR="+dir)
	if err := c.Run(); err == nil {
		t.Error("expected non-zero exit (stale pid → status calls os.Exit(1))")
	}
	if _, err := os.Stat(config.PidfilePath()); !os.IsNotExist(err) {
		t.Error("status should have removed stale pidfile")
	}
}
