package github

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

func TestParseRepoURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r.git":   "o/r",
		"https://github.com/o/r":       "o/r",
		"http://github.com/o/r":        "o/r",
		"git@github.com:o/r.git":       "o/r",
		"https://github.com/o/r/extra": "o/r",
	}
	for in, want := range cases {
		got, err := ParseRepoURL(in)
		if err != nil || got != want {
			t.Errorf("ParseRepoURL(%q) = %q, %v want %q", in, got, err, want)
		}
	}
	bad := []string{"", "https://gitlab.com/o/r", "no-slash", "https://github.com/onlyowner"}
	for _, in := range bad {
		if _, err := ParseRepoURL(in); err == nil {
			t.Errorf("ParseRepoURL(%q) should error", in)
		}
	}
}

// fakeClient routes through an httptest server.
func fakeClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := &Client{token: "tok", http: srv.Client()}
	c.http.Transport = rewriter{base: srv.URL, rt: c.http.Transport}
	return c, srv
}

type rewriter struct {
	base string
	rt   http.RoundTripper
}

func (r rewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), apiBase) {
		newURL := r.base + strings.TrimPrefix(req.URL.String(), apiBase)
		nr, _ := http.NewRequest(req.Method, newURL, req.Body)
		nr.Header = req.Header
		req = nr
	}
	rt := r.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(req)
}

func TestFindOpenPRFound(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"html_url":"https://gh/1","number":1}]`))
	})
	if got := c.FindOpenPR(t.Context(), "o/r", "feat", "main"); got != "https://gh/1" {
		t.Errorf("FindOpenPR = %q", got)
	}
}

func TestFindOpenPRNone(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	if got := c.FindOpenPR(t.Context(), "o/r", "feat", "main"); got != "" {
		t.Errorf("expected empty: %q", got)
	}
}

func TestFindOpenPRBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if got := c.FindOpenPR(t.Context(), "o/r", "feat", "main"); got != "" {
		t.Errorf("expected empty on bad JSON: %q", got)
	}
}

func TestFindOpenPRError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	if got := c.FindOpenPR(t.Context(), "o/r", "feat", "main"); got != "" {
		t.Errorf("expected empty on err: %q", got)
	}
}

func TestCreateOrUpdatePRCreate(t *testing.T) {
	var posted bool
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[]`))
		case http.MethodPost:
			posted = true
			body, _ := io.ReadAll(r.Body)
			var p map[string]any
			_ = json.Unmarshal(body, &p)
			if p["title"] != "T" {
				t.Errorf("post body = %s", body)
			}
			w.Write([]byte(`{"html_url":"https://gh/2"}`))
		}
	})
	got := c.CreateOrUpdatePR(t.Context(), "o/r", "T", "B", "feat", "main")
	if got != "https://gh/2" || !posted {
		t.Errorf("CreateOrUpdatePR = %q posted=%v", got, posted)
	}
}

func TestCreateOrUpdatePRUpdate(t *testing.T) {
	var patched int32
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[{"html_url":"https://gh/1","number":1}]`))
		case http.MethodPatch:
			atomic.AddInt32(&patched, 1)
			w.Write([]byte(`{}`))
		}
	})
	got := c.CreateOrUpdatePR(t.Context(), "o/r", "T", "B", "feat", "main")
	if got != "https://gh/1" {
		t.Errorf("expected existing PR url, got %q", got)
	}
	if atomic.LoadInt32(&patched) != 1 {
		t.Errorf("expected 1 patch call, got %d", patched)
	}
}

func TestCreateOrUpdatePRCreateError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
	})
	if got := c.CreateOrUpdatePR(t.Context(), "o/r", "T", "B", "feat", "main"); got != "" {
		t.Errorf("expected empty on POST error: %q", got)
	}
}

func TestCreateOrUpdatePRCreateBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[]`))
		case http.MethodPost:
			w.Write([]byte("not json"))
		}
	})
	if got := c.CreateOrUpdatePR(t.Context(), "o/r", "T", "B", "feat", "main"); got != "" {
		t.Errorf("expected empty on bad JSON: %q", got)
	}
}

func TestGetWithRetryRetries(t *testing.T) {
	var hits int32
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`[]`))
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	resp, _, err := c.getWithRetry(t.Context(), "/repos/o/r/pulls")
	if err != nil || resp == nil || resp.StatusCode != 200 {
		t.Errorf("expected success after retries: err=%v resp=%v", err, resp)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Errorf("expected 3 calls, got %d", hits)
	}
}

func TestGetWithRetryExhausts(t *testing.T) {
	var hits int32
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	resp, _, _ := c.getWithRetry(t.Context(), "/repos/o/r/pulls")
	if resp == nil || resp.StatusCode != 429 {
		t.Errorf("expected 429 after exhaust, got %v", resp)
	}
	if atomic.LoadInt32(&hits) != 4 {
		t.Errorf("expected 4 calls (initial + 3 retries), got %d", hits)
	}
}

func TestUpdatePRError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	c.updatePR(t.Context(), "o/r", 1, "T", "B")
}

func TestNewWithoutToken(t *testing.T) {
	c := New()
	if c == nil {
		t.Error("New must return non-nil")
	}
}

func TestAddPRCommentOK(t *testing.T) {
	var posted bool
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/comments") {
			posted = true
			body, _ := io.ReadAll(r.Body)
			var p map[string]any
			_ = json.Unmarshal(body, &p)
			if p["body"] != "hi" {
				t.Errorf("body = %v", p)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	})
	if !c.AddPRComment(t.Context(), "o/r", 7, "hi") || !posted {
		t.Errorf("AddPRComment should succeed")
	}
}

func TestAddPRCommentFail(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if c.AddPRComment(t.Context(), "o/r", 1, "hi") {
		t.Error("should return false on 403")
	}
}

func TestAddPRCommentHTTPError(t *testing.T) {
	c := &Client{token: "t", http: &http.Client{Transport: errTransport{}}}
	if c.AddPRComment(t.Context(), "o/r", 1, "hi") {
		t.Error("should return false on transport error")
	}
}

type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errFake
}

var errFake = &fakeError{"boom"}

type fakeError struct{ s string }

func (e *fakeError) Error() string { return e.s }

func TestPRHeadBranchOK(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pulls/9") {
			_, _ = w.Write([]byte(`{"head":{"ref":"PROJ-1"}}`))
			return
		}
		w.WriteHeader(404)
	})
	if got := c.PRHeadBranch(t.Context(), "o/r", 9); got != "PROJ-1" {
		t.Errorf("PRHeadBranch = %q", got)
	}
}

func TestPRHeadBranchErrors(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	if got := c.PRHeadBranch(t.Context(), "o/r", 1); got != "" {
		t.Errorf("expected empty on 403: %q", got)
	}
}

func TestWorkflowRunFailureSummaryHappyPath(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/42/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":1,"name":"build","conclusion":"success"},
				{"id":2,"name":"test","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs/2/logs"):
			_, _ = w.Write([]byte("FAIL: TestX\nexpected 1, got 2\n"))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 42)
	if !strings.Contains(got, "=== job: test (failed) ===") ||
		!strings.Contains(got, "FAIL: TestX") {
		t.Errorf("summary missing parts: %q", got)
	}
}

func TestWorkflowRunFailureSummaryMultipleFailures(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/7/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":10,"name":"lint","conclusion":"failure"},
				{"id":11,"name":"unit","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs/10/logs"):
			_, _ = w.Write([]byte("lint error: unused import"))
		case strings.HasSuffix(r.URL.Path, "/jobs/11/logs"):
			_, _ = w.Write([]byte("unit test FAIL"))
		}
	})
	got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 7)
	if !strings.Contains(got, "lint") || !strings.Contains(got, "unit") {
		t.Errorf("both jobs should appear: %q", got)
	}
	if strings.Index(got, "lint") > strings.Index(got, "unit") {
		t.Errorf("order preserved: %q", got)
	}
}

func TestWorkflowRunFailureSummaryNoFailures(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[{"id":1,"name":"a","conclusion":"success"}]}`))
	})
	if got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 1); got != "" {
		t.Errorf("expected empty: %q", got)
	}
}

func TestWorkflowRunFailureSummaryJobsAPIError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	if got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 1); got != "" {
		t.Errorf("expected empty on API error: %q", got)
	}
}

func TestWorkflowRunFailureSummaryJobsBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	if got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 1); got != "" {
		t.Errorf("expected empty on bad JSON: %q", got)
	}
}

func TestWorkflowRunFailureSummaryLogError(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/1/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":1,"name":"a","conclusion":"failure"},
				{"id":2,"name":"b","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs/1/logs"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/jobs/2/logs"):
			_, _ = w.Write([]byte("job b output"))
		}
	})
	old := getBackoffs
	getBackoffs = []time.Duration{0, 0, 0}
	defer func() { getBackoffs = old }()
	got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 1)
	if strings.Contains(got, "job: a") {
		t.Errorf("failed-log job a should be skipped: %q", got)
	}
	if !strings.Contains(got, "job: b") || !strings.Contains(got, "job b output") {
		t.Errorf("job b should still render: %q", got)
	}
}

func TestWorkflowRunFailureSummaryTruncation(t *testing.T) {
	big := strings.Repeat("x", 5000) + "TAIL_MARKER"
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs/1/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"id":1,"name":"x","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs/1/logs"):
			_, _ = w.Write([]byte(big))
		}
	})
	got := c.WorkflowRunFailureSummary(t.Context(), "o/r", 1)
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("expected truncation marker: %q", got[:min(200, len(got))])
	}
	if !strings.Contains(got, "TAIL_MARKER") {
		t.Error("tail should be preserved")
	}
}

func TestPRHeadBranchBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	if got := c.PRHeadBranch(t.Context(), "o/r", 1); got != "" {
		t.Errorf("expected empty: %q", got)
	}
}
