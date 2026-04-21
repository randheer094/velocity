package arch

import (
	"fmt"

	"github.com/randheer094/velocity/internal/data"
)

// buildWavesComment renders the plan as an ADF ordered list: one numbered
// item per non-empty wave, each containing a nested bullet list of Jira
// keys. Jira auto-linkifies keys in plain-text nodes. Returns nil when no
// wave has a Jira-keyed task so the caller can skip posting.
func buildWavesComment(waves []data.Wave) []any {
	items := make([]any, 0, len(waves))
	visible := 0
	for _, w := range waves {
		keys := make([]string, 0, len(w.Tasks))
		for _, t := range w.Tasks {
			if t.JiraKey != "" {
				keys = append(keys, t.JiraKey)
			}
		}
		if len(keys) == 0 {
			continue
		}
		visible++
		bulletItems := make([]any, 0, len(keys))
		for _, k := range keys {
			bulletItems = append(bulletItems, adfListItem(adfParagraph(k)))
		}
		items = append(items, adfListItem(
			adfParagraph(fmt.Sprintf("Wave %d", visible)),
			map[string]any{"type": "bulletList", "content": bulletItems},
		))
	}
	if len(items) == 0 {
		return nil
	}
	return []any{
		map[string]any{"type": "orderedList", "content": items},
	}
}

func adfParagraph(text string) map[string]any {
	return map[string]any{
		"type":    "paragraph",
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

func adfListItem(children ...map[string]any) map[string]any {
	content := make([]any, 0, len(children))
	for _, c := range children {
		content = append(content, c)
	}
	return map[string]any{"type": "listItem", "content": content}
}
