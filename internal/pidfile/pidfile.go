// Package pidfile reads and writes the velocity daemon pidfile and
// verifies that the recorded pid still belongs to the same velocity
// binary. Used by `velocity stop`, `velocity status`, and
// `velocity update-prompts` to avoid signaling a recycled pid.
package pidfile

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Entry is one parsed pidfile. ExePath is empty when the on-disk file
// uses the legacy single-pid format — callers should treat that as
// "exe verification skipped".
type Entry struct {
	PID     int
	ExePath string
}

// Format renders an entry to the canonical on-disk shape: one line of
// `<pid>\t<exe-path>`. A tab separator round-trips paths that contain
// spaces; legacy "<pid> <exe-path>" files are still accepted by Read.
// ExePath may be empty.
func (e Entry) Format() string {
	if e.ExePath == "" {
		return strconv.Itoa(e.PID)
	}
	return fmt.Sprintf("%d\t%s", e.PID, e.ExePath)
}

// Read parses the pidfile at path. Missing file returns (Entry{}, nil)
// so callers can treat "absent" the same as "stopped". Malformed
// content returns an error. The legacy single-int format is accepted
// for forward compatibility with daemons started by older binaries.
func Read(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, nil
		}
		return Entry{}, err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return Entry{}, fmt.Errorf("empty pidfile")
	}
	// Split on the first whitespace run so a tab-delimited path with
	// embedded spaces round-trips intact. Legacy single-space files
	// also parse correctly because we still split on the first run.
	pidStr, exePath, _ := strings.Cut(line, "\t")
	if exePath == "" {
		// Tab missing → legacy " " format. Fall back to first space.
		pidStr, exePath, _ = strings.Cut(line, " ")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil {
		return Entry{}, fmt.Errorf("invalid pidfile: %w", err)
	}
	return Entry{PID: pid, ExePath: strings.TrimSpace(exePath)}, nil
}

// Write atomically writes entry to path. Atomicity matters because
// other CLI invocations may try to read the pidfile concurrently with
// `velocity start`.
func Write(path string, entry Entry) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(entry.Format()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deletes the pidfile if present. Missing file is not an error.
func Remove(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// VerifyAlive reports whether the recorded daemon is still the
// velocity process at e.PID.
//
// Layered checks:
//  1. Kill(pid, 0) — must report the process exists. ESRCH → dead.
//  2. On Linux only, readlink /proc/<pid>/exe and compare to
//     e.ExePath. A mismatch (or unreadable link) means the pid was
//     recycled by an unrelated process.
//
// On non-Linux platforms (e.g. darwin, the release target), the proc
// check is skipped — there is no portable equivalent in the standard
// library. Callers accept that pid recycling is undetectable on those
// platforms; the Kill(0) check still catches the common "daemon
// exited cleanly" case.
//
// Legacy entries (ExePath == "") skip the proc check on every
// platform, matching the behaviour of the previous single-int pidfile.
func VerifyAlive(e Entry) bool {
	if e.PID == 0 {
		return false
	}
	if err := syscall.Kill(e.PID, 0); err != nil {
		return false
	}
	if runtime.GOOS != "linux" || e.ExePath == "" {
		return true
	}
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", e.PID))
	if err != nil {
		// /proc may not be mounted (e.g. minimal containers). Fall
		// back to the Kill(0) result rather than erroring out.
		return true
	}
	// When the binary on disk is replaced (e.g. `cp velocity ...`
	// while a daemon is running), the kernel marks the exe link as
	// "<original-path> (deleted)". Strip the suffix so an in-place
	// upgrade doesn't false-negative — the running process is still
	// the velocity binary the pidfile recorded, just the on-disk
	// inode has been swapped out.
	return strings.TrimSuffix(link, " (deleted)") == e.ExePath
}
