// Package github is the HTTP client for GitHub's REST API.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

const requestTimeout = 30 * time.Second

// maxResponseBytes caps how much of a GitHub response body the client
// will buffer. PR / Issue / Workflow API replies sit well below this;
// the cap prevents a misbehaving proxy or compromised endpoint from
// exhausting memory with an unbounded stream.
const maxResponseBytes = 16 << 20

// apiBase is overridden by tests via the test helper in client_test.go.
var apiBase = "https://api.github.com"

type Client struct {
	token string
	http  *http.Client
}

// New builds a Client using the GH_TOKEN env var.
func New() *Client {
	token := os.Getenv(config.GithubTokenEnv)
	if token == "" {
		slog.Warn("GH_TOKEN missing; GitHub operations will fail.")
	}
	return &Client{token: token, http: &http.Client{Timeout: requestTimeout}}
}

// 3 GET attempts total on network errors / 429 / 5xx. POSTs and
// PATCHes stay single-shot.
var getBackoffs = []time.Duration{500 * time.Millisecond, 2 * time.Second, 8 * time.Second}

func (c *Client) getWithRetry(ctx context.Context, path string) (*http.Response, []byte, error) {
	var (
		resp *http.Response
		body []byte
		err  error
	)
	for attempt := 0; attempt <= len(getBackoffs); attempt++ {
		resp, body, err = c.do(ctx, http.MethodGet, path, nil)
		if err == nil && resp.StatusCode != 429 && resp.StatusCode < 500 {
			return resp, body, nil
		}
		if attempt == len(getBackoffs) {
			break
		}
		slog.Warn("github GET retrying", "path", path, "attempt", attempt+1)
		if !sleepCtx(ctx, getBackoffs[attempt]) {
			return resp, body, ctx.Err()
		}
	}
	return resp, body, err
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, []byte, error) {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, r)
	if err != nil {
		return nil, nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return resp, respBody, nil
}

// sleepCtx waits for d or until ctx fires. Returns false when ctx
// cancelled before the timer expired so callers can abort early.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// FindOpenPR returns the URL of an open PR with the given head branch, or "".
func (c *Client) FindOpenPR(ctx context.Context, repoFullName, head, base string) string {
	url, _ := c.findOpenPR(ctx, repoFullName, head, base)
	return url
}

func (c *Client) findOpenPR(ctx context.Context, repoFullName, head, base string) (string, int) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("head", strings.SplitN(repoFullName, "/", 2)[0]+":"+head)
	if base != "" {
		q.Set("base", base)
	}
	resp, body, err := c.getWithRetry(ctx, "/repos/"+repoFullName+"/pulls?"+q.Encode())
	if err != nil || resp.StatusCode >= 400 {
		return "", 0
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return "", 0
	}
	if len(out) == 0 {
		return "", 0
	}
	u, _ := out[0]["html_url"].(string)
	num := 0
	if n, ok := out[0]["number"].(float64); ok {
		num = int(n)
	}
	return u, num
}

// CreateOrUpdatePR opens a PR or refreshes an existing open PR on the
// same branch. Returns the html_url.
func (c *Client) CreateOrUpdatePR(ctx context.Context, repoFullName, title, body, head, base string) string {
	if existing, num := c.findOpenPR(ctx, repoFullName, head, base); existing != "" {
		if num > 0 {
			c.updatePR(ctx, repoFullName, num, title, body)
		}
		return existing
	}
	payload := map[string]any{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	}
	resp, respBody, err := c.do(ctx, http.MethodPost, "/repos/"+repoFullName+"/pulls", payload)
	if err != nil {
		slog.Error("github create PR error", "repo", repoFullName, "err", err)
		return ""
	}
	if resp.StatusCode >= 400 {
		slog.Error("github create PR failed", "repo", repoFullName, "status", resp.StatusCode, "body", string(respBody))
		return ""
	}
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return ""
	}
	u, _ := out["html_url"].(string)
	return u
}

func (c *Client) updatePR(ctx context.Context, repoFullName string, number int, title, body string) {
	payload := map[string]any{"title": title, "body": body}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repoFullName, number)
	resp, respBody, err := c.do(ctx, http.MethodPatch, path, payload)
	if err != nil {
		slog.Warn("github update PR error", "repo", repoFullName, "num", number, "err", err)
		return
	}
	if resp.StatusCode >= 400 {
		slog.Warn("github update PR failed", "repo", repoFullName, "num", number, "status", resp.StatusCode, "body", string(respBody))
	}
}

// PRHeadBranch returns the head ref of a PR, or "" on error.
func (c *Client) PRHeadBranch(ctx context.Context, repoFullName string, number int) string {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repoFullName, number)
	resp, body, err := c.getWithRetry(ctx, path)
	if err != nil || resp.StatusCode >= 400 {
		return ""
	}
	var out struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ""
	}
	return out.Head.Ref
}

// AddPRComment posts an issue comment on a pull request. PRs are
// issues in GitHub's REST model, so this uses /issues/{num}/comments.
func (c *Client) AddPRComment(ctx context.Context, repoFullName string, number int, body string) bool {
	payload := map[string]any{"body": body}
	path := fmt.Sprintf("/repos/%s/issues/%d/comments", repoFullName, number)
	resp, respBody, err := c.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		slog.Warn("github add comment error", "repo", repoFullName, "num", number, "err", err)
		return false
	}
	if resp.StatusCode >= 400 {
		slog.Warn("github add comment failed", "repo", repoFullName, "num", number, "status", resp.StatusCode, "body", string(respBody))
		return false
	}
	return true
}

// WorkflowRunFailureSummary fetches the jobs for a workflow run, pulls
// the plain-text log of each failed job, and concatenates a trimmed
// tail of each. Returns "" if the run is unknown, the API errors, or
// nothing failed. Caps per-job and total size so the output fits in
// an LLM prompt.
func (c *Client) WorkflowRunFailureSummary(ctx context.Context, repoFullName string, runID int64) string {
	const (
		perJobCap = 4000
		totalCap  = 8000
	)
	jobs := c.listFailedJobs(ctx, repoFullName, runID)
	if len(jobs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, j := range jobs {
		if b.Len() >= totalCap {
			break
		}
		log := c.jobLog(ctx, repoFullName, j.id)
		if log == "" {
			continue
		}
		if len(log) > perJobCap {
			log = "...[truncated]\n" + log[len(log)-perJobCap:]
		}
		fmt.Fprintf(&b, "=== job: %s (failed) ===\n%s\n\n", j.name, strings.TrimRight(log, "\n"))
	}
	out := b.String()
	if len(out) > totalCap {
		out = out[:totalCap] + "\n...[truncated]\n"
	}
	return out
}

type failedJob struct {
	id   int64
	name string
}

func (c *Client) listFailedJobs(ctx context.Context, repoFullName string, runID int64) []failedJob {
	path := fmt.Sprintf("/repos/%s/actions/runs/%d/jobs", repoFullName, runID)
	resp, body, err := c.getWithRetry(ctx, path)
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	var out struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	failed := make([]failedJob, 0, len(out.Jobs))
	for _, j := range out.Jobs {
		if j.Conclusion == "failure" {
			failed = append(failed, failedJob{id: j.ID, name: j.Name})
		}
	}
	return failed
}

func (c *Client) jobLog(ctx context.Context, repoFullName string, jobID int64) string {
	path := fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", repoFullName, jobID)
	resp, body, err := c.getWithRetry(ctx, path)
	if err != nil || resp.StatusCode >= 400 {
		return ""
	}
	return string(body)
}

// ParseRepoURL turns a GitHub URL into "owner/repo".
func ParseRepoURL(repoURL string) (string, error) {
	u := strings.TrimSpace(repoURL)
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "http://github.com/")
	u = strings.TrimPrefix(u, "git@github.com:")
	if u == "" || !strings.Contains(u, "/") {
		return "", fmt.Errorf("not a github repo url: %q", repoURL)
	}
	parts := strings.SplitN(u, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("not a github repo url: %q", repoURL)
	}
	return parts[0] + "/" + parts[1], nil
}
