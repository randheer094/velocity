package arch

import (
	"fmt"
	"strings"

	"github.com/randheer094/velocity/internal/data"
)

// renderWavesASCII draws the plan as horizontal wave boxes:
//
//	┌────────┐    ┌────────┐
//	│ Wave 1 │ -> │ Wave 2 │
//	├────────┤    ├────────┤
//	│ ABC-1  │    │ ABC-3  │
//	│ ABC-2  │    │        │
//	└────────┘    └────────┘
//
// Empty plans and waves with no Jira-keyed tasks yield "".
func renderWavesASCII(waves []data.Wave) string {
	keys := make([][]string, 0, len(waves))
	for _, w := range waves {
		row := make([]string, 0, len(w.Tasks))
		for _, t := range w.Tasks {
			if t.JiraKey != "" {
				row = append(row, t.JiraKey)
			}
		}
		keys = append(keys, row)
	}
	n := len(keys)
	if n == 0 {
		return ""
	}
	total := 0
	for _, row := range keys {
		total += len(row)
	}
	if total == 0 {
		return ""
	}

	widths := make([]int, n)
	maxRows := 0
	for i, row := range keys {
		label := fmt.Sprintf("Wave %d", i+1)
		inner := len(label)
		for _, k := range row {
			if len(k) > inner {
				inner = len(k)
			}
		}
		widths[i] = inner + 2
		if len(row) > maxRows {
			maxRows = len(row)
		}
	}

	const gap = "    "
	const arrow = " -> "
	var b strings.Builder

	writeBorder := func(left, mid, right string) {
		for i, w := range widths {
			if i > 0 {
				b.WriteString(gap)
			}
			b.WriteString(left)
			b.WriteString(strings.Repeat(mid, w))
			b.WriteString(right)
		}
		b.WriteByte('\n')
	}

	writeCellRow := func(values []string, sep string) {
		for i, w := range widths {
			if i > 0 {
				b.WriteString(sep)
			}
			v := values[i]
			b.WriteString("│ ")
			b.WriteString(v)
			b.WriteString(strings.Repeat(" ", w-len(v)-2))
			b.WriteString(" │")
		}
		b.WriteByte('\n')
	}

	writeBorder("┌", "─", "┐")
	labels := make([]string, n)
	for i := range widths {
		labels[i] = fmt.Sprintf("Wave %d", i+1)
	}
	writeCellRow(labels, arrow)
	writeBorder("├", "─", "┤")
	for r := 0; r < maxRows; r++ {
		row := make([]string, n)
		for i := range widths {
			if r < len(keys[i]) {
				row[i] = keys[i][r]
			}
		}
		writeCellRow(row, gap)
	}
	writeBorder("└", "─", "┘")
	return strings.TrimRight(b.String(), "\n")
}
