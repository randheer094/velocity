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
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"key":"NEW-99"}`))
	})
	got := c.CreateSubtask("PROJ", "title", "desc", "PROJ-1")
	if got != "NEW-99" {
		t.Errorf("CreateSubtask = %q", got)
	}
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

func TestGetProjectStatuses(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{
          "statuses": [
            {"name":"To Do","statusCategory":{"key":"new"}},
            {"name":"Done","statusCategory":{"key":"done"}}
          ]
        }, {
          "statuses": [
            {"name":"To Do"},
            {"name":"Backlog"}
          ]
        }]`))
	})
	got := c.GetProjectStatuses("PROJ")
	if len(got) != 3 {
		t.Errorf("expected 3 unique statuses, got %v", got)
	}
}

func TestGetProjectStatusesBadShape(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	if got := c.GetProjectStatuses("PROJ"); got != nil {
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
