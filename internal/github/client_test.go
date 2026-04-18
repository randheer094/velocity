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
	if got := c.FindOpenPR("o/r", "feat", "main"); got != "https://gh/1" {
		t.Errorf("FindOpenPR = %q", got)
	}
}

func TestFindOpenPRNone(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	if got := c.FindOpenPR("o/r", "feat", "main"); got != "" {
		t.Errorf("expected empty: %q", got)
	}
}

func TestFindOpenPRBadJSON(t *testing.T) {
	c, _ := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	if got := c.FindOpenPR("o/r", "feat", "main"); got != "" {
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
	if got := c.FindOpenPR("o/r", "feat", "main"); got != "" {
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
	got := c.CreateOrUpdatePR("o/r", "T", "B", "feat", "main")
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
	got := c.CreateOrUpdatePR("o/r", "T", "B", "feat", "main")
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
	if got := c.CreateOrUpdatePR("o/r", "T", "B", "feat", "main"); got != "" {
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
	if got := c.CreateOrUpdatePR("o/r", "T", "B", "feat", "main"); got != "" {
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
	resp, _, err := c.getWithRetry("/repos/o/r/pulls")
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
	resp, _, _ := c.getWithRetry("/repos/o/r/pulls")
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
	c.updatePR("o/r", 1, "T", "B")
}

func TestNewWithoutToken(t *testing.T) {
	c := New()
	if c == nil {
		t.Error("New must return non-nil")
	}
}
