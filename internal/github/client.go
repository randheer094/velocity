// Package github is the HTTP client for GitHub's REST API.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

const requestTimeout = 30 * time.Second

// apiBase is overridden by tests via the test helper in client_test.go.
var apiBase = "https://api.github.com"

type Client struct {
	token string
	http  *http.Client
}

// New builds a Client using GITHUB_TOKEN from the keyring.
func New() *Client {
	token, _ := config.GetSecret(config.GithubTokenKey)
	if token == "" {
		slog.Warn("GITHUB_TOKEN missing; GitHub operations will fail.")
	}
	return &Client{token: token, http: &http.Client{Timeout: requestTimeout}}
}

// 3 GET attempts total on network errors / 429 / 5xx. POSTs and
// PATCHes stay single-shot.
var getBackoffs = []time.Duration{500 * time.Millisecond, 2 * time.Second, 8 * time.Second}

func (c *Client) getWithRetry(path string) (*http.Response, []byte, error) {
	var (
		resp *http.Response
		body []byte
		err  error
	)
	for attempt := 0; attempt <= len(getBackoffs); attempt++ {
		resp, body, err = c.do(http.MethodGet, path, nil)
		if err == nil && resp.StatusCode != 429 && resp.StatusCode < 500 {
			return resp, body, nil
		}
		if attempt == len(getBackoffs) {
			break
		}
		slog.Warn("github GET retrying", "path", path, "attempt", attempt+1)
		time.Sleep(getBackoffs[attempt])
	}
	return resp, body, err
}

func (c *Client) do(method, path string, body any) (*http.Response, []byte, error) {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, apiBase+path, r)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
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
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody, nil
}

// FindOpenPR returns the URL of an open PR with the given head branch, or "".
func (c *Client) FindOpenPR(repoFullName, head, base string) string {
	url, _ := c.findOpenPR(repoFullName, head, base)
	return url
}

func (c *Client) findOpenPR(repoFullName, head, base string) (string, int) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("head", strings.SplitN(repoFullName, "/", 2)[0]+":"+head)
	if base != "" {
		q.Set("base", base)
	}
	resp, body, err := c.getWithRetry("/repos/" + repoFullName + "/pulls?" + q.Encode())
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
func (c *Client) CreateOrUpdatePR(repoFullName, title, body, head, base string) string {
	if existing, num := c.findOpenPR(repoFullName, head, base); existing != "" {
		if num > 0 {
			c.updatePR(repoFullName, num, title, body)
		}
		return existing
	}
	payload := map[string]any{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	}
	resp, respBody, err := c.do(http.MethodPost, "/repos/"+repoFullName+"/pulls", payload)
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

func (c *Client) updatePR(repoFullName string, number int, title, body string) {
	payload := map[string]any{"title": title, "body": body}
	path := fmt.Sprintf("/repos/%s/pulls/%d", repoFullName, number)
	resp, respBody, err := c.do(http.MethodPatch, path, payload)
	if err != nil {
		slog.Warn("github update PR error", "repo", repoFullName, "num", number, "err", err)
		return
	}
	if resp.StatusCode >= 400 {
		slog.Warn("github update PR failed", "repo", repoFullName, "num", number, "status", resp.StatusCode, "body", string(respBody))
	}
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
