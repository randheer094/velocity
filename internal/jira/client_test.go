package jira

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectKeyOf(t *testing.T) {
	cases := map[string]string{
		"ENG-101":  "ENG",
		"PROJ-1":   "PROJ",
		"NOPE":     "",
		"":         "",
		"-3":       "",
		"A-B-C-1":  "A",
	}
	for in, want := range cases {
		if got := ProjectKeyOf(in); got != want {
			t.Errorf("ProjectKeyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 100); got != "hello" {
		t.Errorf("truncate short: %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long: %q", got)
	}
}

func TestShouldRetryGet(t *testing.T) {
	if !shouldRetryGet(nil, io.EOF) {
		t.Error("network err should retry")
	}
	if !shouldRetryGet(&http.Response{StatusCode: 429}, nil) {
		t.Error("429 should retry")
	}
	if !shouldRetryGet(&http.Response{StatusCode: 500}, nil) {
		t.Error("500 should retry")
	}
	if shouldRetryGet(&http.Response{StatusCode: 200}, nil) {
		t.Error("200 should not retry")
	}
	if shouldRetryGet(&http.Response{StatusCode: 404}, nil) {
		t.Error("404 should not retry")
	}
}

func TestFlattenADF(t *testing.T) {
	if got := FlattenADF(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	if got := FlattenADF("plain"); got != "plain" {
		t.Errorf("string = %q", got)
	}
	if got := FlattenADF(123); got != "" {
		t.Errorf("non-map non-string = %q", got)
	}
	doc := map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{"type": "paragraph", "content": []any{
				map[string]any{"type": "text", "text": "hello "},
				map[string]any{"type": "text", "text": "world"},
			}},
			map[string]any{"type": "paragraph", "content": []any{
				map[string]any{"type": "text", "text": "second"},
			}},
		},
	}
	got := FlattenADF(doc)
	if !strings.Contains(got, "hello world") || !strings.Contains(got, "second") {
		t.Errorf("ADF flatten = %q", got)
	}
}

// fakeServer wires a Client to an httptest.Server.
func fakeClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewWithCreds(srv.URL, "u@x", "tok")
	return c, srv
}

func TestClientURL(t *testing.T) {
	c := NewWithCreds("https://j.example.com/", "e", "t")
	got := c.url("issue/X-1", nil)
	want := "https://j.example.com/rest/api/3/issue/X-1"
	if got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
	got2 := c.url("/issue/X-1", nil)
	if got2 != want {
		t.Errorf("url leading / = %q", got2)
	}
}

func TestGetIssue(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issue/X-1") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"key":"X-1","fields":{"summary":"S"}}`))
	})
	got := c.GetIssue("X-1")
	if got == nil || got["key"] != "X-1" {
		t.Errorf("GetIssue = %v", got)
	}
}

func TestGetIssueBrief(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"key":"X-1","fields":{"status":{"name":"In Progress"},"assignee":{"accountId":"u1"}}}`))
	})
	got := c.GetIssueBrief("X-1")
	if got == nil || got.Status != "In Progress" || got.AssigneeAccountID != "u1" {
		t.Errorf("GetIssueBrief = %+v", got)
	}
}

func TestGetIssueBriefBadShape(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	if got := c.GetIssueBrief("X-1"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestGetTasksStatus(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issues":[
          {"key":"A-1","fields":{"status":{"name":"Done"},"assignee":{"accountId":"x"}}},
          {"key":"A-2","fields":{"status":{"name":"To Do"}}}
        ]}`))
	})
	got := c.GetTasksStatus([]string{"A-1", "A-2"})
	if len(got) != 2 || got["A-1"].Status != "Done" {
		t.Errorf("GetTasksStatus = %v", got)
	}
}

func TestGetTasksStatusEmpty(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call server")
	})
	got := c.GetTasksStatus(nil)
	if len(got) != 0 {
		t.Errorf("expected empty: %v", got)
	}
}

func TestAssign(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["accountId"] != "user-1" {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if !c.Assign("X-1", "user-1") {
		t.Error("Assign returned false")
	}
}

func TestAssignFails(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	if c.Assign("X-1", "user-1") {
		t.Error("Assign should return false on 400")
	}
}

func TestTransition(t *testing.T) {
	calls := atomic.Int32{}
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "transitions") && r.Method == http.MethodGet {
			w.Write([]byte(`{"transitions":[
              {"id":"11","to":{"name":"Done"}},
              {"id":"22","to":{"name":"In Progress"}}
            ]}`))
			return
		}
		if strings.Contains(r.URL.Path, "transitions") && r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"id":"11"`) {
				t.Errorf("body = %s", body)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	})
	if !c.Transition("X-1", "Done") {
		t.Error("Transition returned false")
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", calls.Load())
	}
}

func TestTransitionEmptyTarget(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call")
	})
	if c.Transition("X-1", "") {
		t.Error("should return false")
	}
}

func TestTransitionTargetMissing(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"transitions":[{"id":"11","to":{"name":"Done"}}]}`))
	})
	if c.Transition("X-1", "Other") {
		t.Error("should return false when target missing")
	}
}

func TestCreateSubtask(t *testing.T) {
	var gotBody []byte
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"key":"NEW-99"}`))
	})
	desc := "Goal: do it\n\n- one\n- two"
	got := c.CreateSubtask("PROJ", "title", desc, "PROJ-1")
	if got != "NEW-99" {
		t.Errorf("CreateSubtask = %q", got)
	}
	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fields := parsed["fields"].(map[string]any)
	d := fields["description"].(map[string]any)
	if d["type"] != "doc" {
		t.Errorf("description type = %v", d["type"])
	}
	content := d["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %v", len(content), content)
	}
	if content[0].(map[string]any)["type"] != "paragraph" {
		t.Errorf("first block type = %v", content[0])
	}
	if content[1].(map[string]any)["type"] != "bulletList" {
		t.Errorf("second block type = %v", content[1])
	}
}

func TestTextToADF(t *testing.T) {
	t.Run("empty yields single empty paragraph", func(t *testing.T) {
		got := textToADF("")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		if got[0].(map[string]any)["type"] != "paragraph" {
			t.Errorf("type = %v", got[0])
		}
	})

	t.Run("single paragraph", func(t *testing.T) {
		got := textToADF("hello world")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		p := got[0].(map[string]any)
		if p["type"] != "paragraph" {
			t.Errorf("type = %v", p["type"])
		}
		nodes := p["content"].([]any)
		if len(nodes) != 1 || nodes[0].(map[string]any)["text"] != "hello world" {
			t.Errorf("nodes = %v", nodes)
		}
	})

	t.Run("two blocks separated by blank line", func(t *testing.T) {
		got := textToADF("first\n\nsecond")
		if len(got) != 2 {
			t.Fatalf("len = %d", len(got))
		}
		if got[0].(map[string]any)["type"] != "paragraph" || got[1].(map[string]any)["type"] != "paragraph" {
			t.Errorf("got = %v", got)
		}
	})

	t.Run("bullet block becomes bulletList", func(t *testing.T) {
		got := textToADF("- one\n- two\n- three")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		list := got[0].(map[string]any)
		if list["type"] != "bulletList" {
			t.Errorf("type = %v", list["type"])
		}
		items := list["content"].([]any)
		if len(items) != 3 {
			t.Fatalf("items = %d", len(items))
		}
		first := items[0].(map[string]any)
		if first["type"] != "listItem" {
			t.Errorf("item type = %v", first["type"])
		}
		inner := first["content"].([]any)[0].(map[string]any)
		if inner["type"] != "paragraph" {
			t.Errorf("inner = %v", inner)
		}
		textNode := inner["content"].([]any)[0].(map[string]any)
		if textNode["text"] != "one" {
			t.Errorf("bullet text = %v", textNode)
		}
	})

	t.Run("mixed paragraph and bullet list", func(t *testing.T) {
		got := textToADF("Goal: do thing\n\n- a\n- b")
		if len(got) != 2 {
			t.Fatalf("len = %d", len(got))
		}
		if got[0].(map[string]any)["type"] != "paragraph" {
			t.Errorf("first = %v", got[0])
		}
		if got[1].(map[string]any)["type"] != "bulletList" {
			t.Errorf("second = %v", got[1])
		}
	})

	t.Run("multi-line non-bullet block uses hardBreak", func(t *testing.T) {
		got := textToADF("one\ntwo")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		p := got[0].(map[string]any)
		nodes := p["content"].([]any)
		if len(nodes) != 3 {
			t.Fatalf("nodes = %v", nodes)
		}
		if nodes[0].(map[string]any)["text"] != "one" {
			t.Errorf("n0 = %v", nodes[0])
		}
		if nodes[1].(map[string]any)["type"] != "hardBreak" {
			t.Errorf("n1 = %v", nodes[1])
		}
		if nodes[2].(map[string]any)["text"] != "two" {
			t.Errorf("n2 = %v", nodes[2])
		}
	})

	t.Run("mixed non-bullet lines produce paragraph, not list", func(t *testing.T) {
		got := textToADF("- bullet\nprose")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		if got[0].(map[string]any)["type"] != "paragraph" {
			t.Errorf("type = %v", got[0])
		}
	})

	t.Run("leading blank lines ignored", func(t *testing.T) {
		got := textToADF("\n\nhello")
		if len(got) != 1 {
			t.Fatalf("len = %d", len(got))
		}
		if got[0].(map[string]any)["type"] != "paragraph" {
			t.Errorf("type = %v", got[0])
		}
	})
}

func TestCreateSubtaskFails(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	got := c.CreateSubtask("PROJ", "title", "desc", "PROJ-1")
	if got != "" {
		t.Errorf("expected empty: %q", got)
	}
}

func TestCommentIssue(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	if !c.CommentIssue("X-1", "hi") {
		t.Error("expected true")
	}
}

func TestCommentIssueCode(t *testing.T) {
	var seen map[string]any
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Write([]byte(`{}`))
	})
	if !c.CommentIssueCode("X-1", "│ ABC-1 │") {
		t.Error("expected true")
	}
	body, _ := seen["body"].(map[string]any)
	content, _ := body["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("unexpected content: %v", content)
	}
	node, _ := content[0].(map[string]any)
	if node["type"] != "codeBlock" {
		t.Errorf("expected codeBlock, got %v", node["type"])
	}
}

func TestCommentIssueCodeBadPost(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if c.CommentIssueCode("X-1", "hi") {
		t.Error("expected false on bad JSON post")
	}
}

func TestCommentIssueADF(t *testing.T) {
	var seen map[string]any
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Write([]byte(`{}`))
	})
	nodes := []any{
		map[string]any{
			"type": "orderedList",
			"content": []any{
				map[string]any{
					"type": "listItem",
					"content": []any{
						map[string]any{
							"type":    "paragraph",
							"content": []any{map[string]any{"type": "text", "text": "Wave 1"}},
						},
					},
				},
			},
		},
	}
	if !c.CommentIssueADF("X-1", nodes) {
		t.Fatal("expected true")
	}
	body, _ := seen["body"].(map[string]any)
	if body["type"] != "doc" {
		t.Errorf("body type = %v, want doc", body["type"])
	}
	if v, _ := body["version"].(float64); v != 1 {
		t.Errorf("body version = %v, want 1", body["version"])
	}
	content, _ := body["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	node, _ := content[0].(map[string]any)
	if node["type"] != "orderedList" {
		t.Errorf("content[0].type = %v, want orderedList", node["type"])
	}
}

func TestCommentIssueADFBadPost(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if c.CommentIssueADF("X-1", []any{}) {
		t.Error("expected false on bad JSON post")
	}
}

func TestListSubtasks(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"fields":{"subtasks":[{"key":"S-1"},{"key":"S-2"},{"key":""}]}}`))
	})
	got := c.ListSubtasks("X-1")
	if len(got) != 2 || got[0] != "S-1" {
		t.Errorf("ListSubtasks = %v", got)
	}
}

func TestListSubtasksBadShape(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`null`))
	})
	if got := c.ListSubtasks("X-1"); got != nil {
		t.Errorf("expected nil: %v", got)
	}
}

func TestNewFromConfig(t *testing.T) {
	// no config; client should be built with empty fields and warn
	c := New()
	if c == nil {
		t.Error("New should always return non-nil")
	}
}

func TestSharedReinit(t *testing.T) {
	if Shared() != nil {
		// reset for safety
	}
	Reinit()
	if Shared() == nil {
		t.Error("Shared should be non-nil after Reinit")
	}
}

func TestGetServerError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	if got := c.GetIssue("X-1"); got != nil {
		t.Errorf("GetIssue should be nil on 500: %v", got)
	}
}

func TestGetClientError(t *testing.T) {
	c := NewWithCreds("http://127.0.0.1:1", "u", "t")
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	if got := c.GetIssue("X-1"); got != nil {
		t.Errorf("expected nil on network error: %v", got)
	}
}

func TestGet404Returns(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if got := c.GetIssue("X-1"); got != nil {
		t.Errorf("expected nil on 404: %v", got)
	}
}

func TestPostNetworkError(t *testing.T) {
	c := NewWithCreds("http://127.0.0.1:1", "u", "t")
	if got := c.CreateSubtask("PROJ", "t", "d", "P-1"); got != "" {
		t.Errorf("expected empty: %q", got)
	}
}

func TestPostNoContent(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// CommentIssue uses post; 204 should still return non-nil → CommentIssue true.
	if !c.CommentIssue("X-1", "hi") {
		t.Error("expected true on 204")
	}
}

func TestPostBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if c.CommentIssue("X-1", "hi") {
		t.Error("expected false on bad JSON post")
	}
}

func TestPutNetworkError(t *testing.T) {
	c := NewWithCreds("http://127.0.0.1:1", "u", "t")
	if c.Assign("X-1", "u") {
		t.Error("expected false on network error")
	}
}

func TestGetBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if got := c.GetIssue("X-1"); got != nil {
		t.Errorf("expected nil on bad JSON: %v", got)
	}
}
