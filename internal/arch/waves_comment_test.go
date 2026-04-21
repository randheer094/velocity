package arch

import (
	"testing"

	"github.com/randheer094/velocity/internal/data"
)

func TestBuildWavesCommentEmpty(t *testing.T) {
	if got := buildWavesComment(nil); got != nil {
		t.Errorf("nil waves: got %v", got)
	}
	if got := buildWavesComment([]data.Wave{}); got != nil {
		t.Errorf("empty waves: got %v", got)
	}
	noKeys := []data.Wave{{Tasks: []data.PlannedTask{{Title: "x"}}}}
	if got := buildWavesComment(noKeys); got != nil {
		t.Errorf("tasks without keys: got %v", got)
	}
}

func TestBuildWavesCommentShape(t *testing.T) {
	waves := []data.Wave{
		{Tasks: []data.PlannedTask{
			{JiraKey: "ABC-1"},
			{JiraKey: "ABC-2"},
		}},
		{Tasks: []data.PlannedTask{
			{JiraKey: "ABC-3"},
		}},
	}
	content := buildWavesComment(waves)
	if len(content) != 1 {
		t.Fatalf("top content len = %d, want 1", len(content))
	}
	list := asNode(t, content[0])
	if list["type"] != "orderedList" {
		t.Fatalf("top type = %v, want orderedList", list["type"])
	}
	items := asSlice(t, list["content"])
	if len(items) != 2 {
		t.Fatalf("orderedList items = %d, want 2", len(items))
	}
	wantWave(t, items[0], "Wave 1", []string{"ABC-1", "ABC-2"})
	wantWave(t, items[1], "Wave 2", []string{"ABC-3"})
}

func TestBuildWavesCommentSkipsEmptyWaves(t *testing.T) {
	waves := []data.Wave{
		{Tasks: []data.PlannedTask{{JiraKey: "ABC-1"}}},
		{Tasks: []data.PlannedTask{{Title: "planned, not created"}}},
		{Tasks: []data.PlannedTask{{JiraKey: "ABC-2"}}},
	}
	content := buildWavesComment(waves)
	list := asNode(t, content[0])
	items := asSlice(t, list["content"])
	if len(items) != 2 {
		t.Fatalf("orderedList items = %d, want 2", len(items))
	}
	wantWave(t, items[0], "Wave 1", []string{"ABC-1"})
	wantWave(t, items[1], "Wave 2", []string{"ABC-2"})
}

func asNode(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", v)
	}
	return m
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", v)
	}
	return s
}

func wantWave(t *testing.T, listItem any, label string, keys []string) {
	t.Helper()
	item := asNode(t, listItem)
	if item["type"] != "listItem" {
		t.Errorf("item type = %v, want listItem", item["type"])
	}
	children := asSlice(t, item["content"])
	if len(children) != 2 {
		t.Fatalf("listItem children = %d, want 2 (paragraph + bulletList)", len(children))
	}
	if got := paragraphText(t, children[0]); got != label {
		t.Errorf("label = %q, want %q", got, label)
	}
	bullet := asNode(t, children[1])
	if bullet["type"] != "bulletList" {
		t.Errorf("bullet type = %v, want bulletList", bullet["type"])
	}
	bulletItems := asSlice(t, bullet["content"])
	if len(bulletItems) != len(keys) {
		t.Fatalf("bullet items = %d, want %d", len(bulletItems), len(keys))
	}
	for i, want := range keys {
		li := asNode(t, bulletItems[i])
		liChildren := asSlice(t, li["content"])
		if len(liChildren) != 1 {
			t.Fatalf("bullet[%d] children = %d, want 1", i, len(liChildren))
		}
		if got := paragraphText(t, liChildren[0]); got != want {
			t.Errorf("bullet[%d] text = %q, want %q", i, got, want)
		}
	}
}

func paragraphText(t *testing.T, v any) string {
	t.Helper()
	p := asNode(t, v)
	if p["type"] != "paragraph" {
		t.Fatalf("expected paragraph, got %v", p["type"])
	}
	children := asSlice(t, p["content"])
	if len(children) != 1 {
		t.Fatalf("paragraph children = %d, want 1", len(children))
	}
	text := asNode(t, children[0])
	if text["type"] != "text" {
		t.Fatalf("expected text, got %v", text["type"])
	}
	s, _ := text["text"].(string)
	return s
}
