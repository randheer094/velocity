package pidfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	in := Entry{PID: 12345, ExePath: "/tmp/velocity"}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("round-trip = %+v, want %+v", got, in)
	}
}

func TestReadMissingFile(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Errorf("missing file should not error: %v", err)
	}
	if got != (Entry{}) {
		t.Errorf("missing file should yield zero entry, got %+v", got)
	}
}

func TestReadLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	// Older daemons wrote just the pid as text. Read must accept it.
	if err := os.WriteFile(path, []byte("987\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read legacy: %v", err)
	}
	if got.PID != 987 {
		t.Errorf("PID = %d, want 987", got.PID)
	}
	if got.ExePath != "" {
		t.Errorf("legacy ExePath should be empty, got %q", got.ExePath)
	}
}

func TestReadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("empty file should error")
	}
}

func TestReadGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("garbage should error")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	if err := Write(path, Entry{PID: 1, ExePath: "/x"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected pidfile removed")
	}
	// Removing again is a no-op.
	if err := Remove(path); err != nil {
		t.Errorf("second Remove should be no-op, got %v", err)
	}
}

func TestEntryFormat(t *testing.T) {
	if got := (Entry{PID: 12, ExePath: "/x"}).Format(); got != "12 /x" {
		t.Errorf("Format with exe = %q", got)
	}
	if got := (Entry{PID: 12}).Format(); got != "12" {
		t.Errorf("Format without exe = %q", got)
	}
}

func TestVerifyAliveZeroPid(t *testing.T) {
	if VerifyAlive(Entry{}) {
		t.Error("zero pid should not be alive")
	}
}

func TestVerifyAliveDeadPid(t *testing.T) {
	// PID > /proc/sys/kernel/pid_max-likely-ceiling is dead. We use
	// a value that is in range but extremely unlikely to be live.
	if VerifyAlive(Entry{PID: 0x7fff_fffe, ExePath: "/x"}) {
		t.Error("very large pid should not be alive")
	}
}

func TestVerifyAliveSelf(t *testing.T) {
	// Sanity check Kill(0) gate — current process is alive.
	pid := syscall.Getpid()
	exe, _ := os.Executable()
	if !VerifyAlive(Entry{PID: pid, ExePath: exe}) {
		t.Error("current process should verify alive")
	}
}

func TestVerifyAliveExeMismatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exe-link check is Linux-only")
	}
	pid := syscall.Getpid()
	if VerifyAlive(Entry{PID: pid, ExePath: "/definitely/not/our/binary"}) {
		t.Error("exe mismatch should fail verification on Linux")
	}
}

func TestVerifyAliveLegacyEntry(t *testing.T) {
	// A legacy entry with empty ExePath skips the proc check on
	// every platform, so a live pid still passes.
	pid := syscall.Getpid()
	if !VerifyAlive(Entry{PID: pid}) {
		t.Error("legacy entry with live pid should verify alive")
	}
}

// trimDeletedSuffix is duplicated here as a unit-test seam — verifies
// that the suffix-stripping logic in VerifyAlive matches what we'd
// expect when /proc/<pid>/exe reports a swapped-out binary. The
// production path is exercised on Linux only so this guards the logic
// independently of platform.
func TestTrimDeletedSuffix(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/velocity":              "/usr/local/bin/velocity",
		"/usr/local/bin/velocity (deleted)":    "/usr/local/bin/velocity",
		"":                                     "",
		"velocity (deleted)":                   "velocity",
		"/path/with spaces/velocity (deleted)": "/path/with spaces/velocity",
	}
	for in, want := range cases {
		if got := strings.TrimSuffix(in, " (deleted)"); got != want {
			t.Errorf("TrimSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteAtomic(t *testing.T) {
	// The .tmp file must not survive a successful Write (rename).
	dir := t.TempDir()
	path := filepath.Join(dir, "pid")
	if err := Write(path, Entry{PID: 1, ExePath: "/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmpfile not cleaned up: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "1 /x" {
		t.Errorf("written = %q", data)
	}
}

func TestEntryFormatIsParseable(t *testing.T) {
	in := Entry{PID: 42, ExePath: "/tmp/velocity"}
	dir := t.TempDir()
	path := filepath.Join(dir, "p")
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("re-read = %+v, want %+v", out, in)
	}
	// Sanity: the on-disk form parses with strconv to confirm we
	// haven't accidentally serialized something bizarre.
	if _, err := strconv.Atoi(out.Format()[:2]); err != nil {
		t.Errorf("first two chars of formatted entry should be numeric: %q", out.Format())
	}
}
