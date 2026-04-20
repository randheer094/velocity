package arch

import (
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/data"
)

func TestRenderWavesASCIIEmpty(t *testing.T) {
	if got := renderWavesASCII(nil); got != "" {
		t.Errorf("nil waves: got %q", got)
	}
	if got := renderWavesASCII([]data.Wave{}); got != "" {
		t.Errorf("empty waves: got %q", got)
	}
	// waves present but no keys → blank
	noKeys := []data.Wave{{Tasks: []data.PlannedTask{{Title: "x"}}}}
	if got := renderWavesASCII(noKeys); got != "" {
		t.Errorf("tasks without keys: got %q", got)
	}
}

func TestRenderWavesASCIIShape(t *testing.T) {
	waves := []data.Wave{
		{Tasks: []data.PlannedTask{
			{JiraKey: "ABC-1"},
			{JiraKey: "ABC-2"},
		}},
		{Tasks: []data.PlannedTask{
			{JiraKey: "ABC-3"},
		}},
	}
	got := renderWavesASCII(waves)
	lines := strings.Split(got, "\n")
	// top + header + sep + 2 rows + bottom = 6 lines
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d:\n%s", len(lines), got)
	}
	// Every line must be the same visible width (same count of runes).
	want := len([]rune(lines[0]))
	for i, l := range lines {
		if got := len([]rune(l)); got != want {
			t.Errorf("line %d width %d, want %d: %q", i, got, want, l)
		}
	}
	if !strings.Contains(lines[1], "Wave 1") || !strings.Contains(lines[1], "Wave 2") {
		t.Errorf("header missing wave labels: %q", lines[1])
	}
	if !strings.Contains(lines[1], " -> ") {
		t.Errorf("expected arrow between waves: %q", lines[1])
	}
	if !strings.Contains(lines[3], "ABC-1") || !strings.Contains(lines[3], "ABC-3") {
		t.Errorf("first task row missing keys: %q", lines[3])
	}
	if !strings.Contains(lines[4], "ABC-2") {
		t.Errorf("second row missing ABC-2: %q", lines[4])
	}
}

func TestRenderWavesASCIIWidthDrivenByLongestKey(t *testing.T) {
	waves := []data.Wave{
		{Tasks: []data.PlannedTask{
			{JiraKey: "VERYLONGPROJECT-1234"},
		}},
	}
	got := renderWavesASCII(waves)
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[3], "VERYLONGPROJECT-1234") {
		t.Errorf("long key missing: %q", lines[3])
	}
	// Top border must stretch to cover the key (key + 2 padding + corners).
	if len([]rune(lines[0])) < len("VERYLONGPROJECT-1234")+4 {
		t.Errorf("top border too short: %q", lines[0])
	}
}
