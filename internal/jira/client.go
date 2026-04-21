// Package jira is the HTTP client for Atlassian Jira REST API v3.
package jira

import (
	"bytes"
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
	"github.com/randheer094/velocity/internal/status"
)

const (
	apiPath        = "/rest/api/3/"
	maxLogBody     = 200
	requestTimeout = 30 * time.Second
)

type Client struct {
	baseURL string
	email   string
	token   string
	http    *http.Client
}

// New builds a Client from loaded config + JIRA_API_TOKEN env var.
// Warns on missing creds so a partial install can still boot.
func New() *Client {
	cfg := config.Get()
	baseURL := ""
	email := ""
	if cfg != nil {
		baseURL = cfg.Jira.BaseURL
		email = cfg.Jira.Email
	}
	token := os.Getenv(config.JiraTokenEnv)

	if baseURL == "" {
		slog.Warn("jira.base_url not set; Jira operations will fail.")
	}
	if email == "" || token == "" {
		slog.Warn("jira email or JIRA_API_TOKEN missing; Jira operations will fail.")
	}
	return NewWithCreds(baseURL, email, token)
}

// NewWithCreds builds a Client from explicit creds. Used by tests and
// by New() after reading config + env.
func NewWithCreds(baseURL, email, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		email:   email,
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

func (c *Client) url(path string, query url.Values) string {
	u := c.baseURL + apiPath + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *Client) do(method, path string, query url.Values, body any) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.url(path, query), bodyReader)
	if err != nil {
		return nil, nil, err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
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

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// 3 GET attempts total on network errors / 429 / 5xx. POSTs and PUTs
// stay single-shot — not generally retry-safe.
var getBackoffs = []time.Duration{500 * time.Millisecond, 2 * time.Second, 8 * time.Second}

func (c *Client) get(path string, query url.Values) any {
	var (
		resp *http.Response
		body []byte
		err  error
	)
	for attempt := 0; attempt <= len(getBackoffs); attempt++ {
		resp, body, err = c.do(http.MethodGet, path, query, nil)
		if !shouldRetryGet(resp, err) {
			break
		}
		if attempt == len(getBackoffs) {
			break
		}
		slog.Warn("jira GET retrying", "url", c.url(path, query), "attempt", attempt+1)
		time.Sleep(getBackoffs[attempt])
	}
	if err != nil {
		slog.Error("jira GET error", "url", c.url(path, query), "err", err)
		return nil
	}
	if resp.StatusCode >= 400 {
		slog.Error("jira GET failed", "url", c.url(path, query), "status", resp.StatusCode, "body", truncate(string(body), maxLogBody))
		return nil
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		slog.Error("jira GET decode failed", "url", c.url(path, query), "err", err)
		return nil
	}
	return out
}

func shouldRetryGet(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp.StatusCode == 429 || resp.StatusCode >= 500
}

func (c *Client) post(path string, payload any) any {
	resp, body, err := c.do(http.MethodPost, path, nil, payload)
	if err != nil {
		slog.Error("jira POST error", "url", c.url(path, nil), "err", err)
		return nil
	}
	if resp.StatusCode >= 400 {
		slog.Error("jira POST failed", "url", c.url(path, nil), "status", resp.StatusCode, "body", truncate(string(body), maxLogBody))
		return nil
	}
	if resp.StatusCode == http.StatusNoContent || len(body) == 0 {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		slog.Error("jira POST decode failed", "url", c.url(path, nil), "err", err)
		return nil
	}
	return out
}

func (c *Client) put(path string, payload any) bool {
	resp, body, err := c.do(http.MethodPut, path, nil, payload)
	if err != nil {
		slog.Error("jira PUT error", "url", c.url(path, nil), "err", err)
		return false
	}
	if resp.StatusCode >= 400 {
		slog.Error("jira PUT failed", "url", c.url(path, nil), "status", resp.StatusCode, "body", truncate(string(body), maxLogBody))
		return false
	}
	return true
}

func (c *Client) GetIssue(key string) map[string]any {
	raw := c.get("issue/"+key, nil)
	root, _ := raw.(map[string]any)
	return root
}

func (c *Client) GetIssueBrief(key string) *status.IssueInfo {
	raw := c.get("issue/"+key, url.Values{"fields": {"status,assignee"}})
	root, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	fields, _ := root["fields"].(map[string]any)
	var statusName, assigneeID string
	if st, ok := fields["status"].(map[string]any); ok {
		statusName, _ = st["name"].(string)
	}
	if assignee, ok := fields["assignee"].(map[string]any); ok {
		assigneeID, _ = assignee["accountId"].(string)
	}
	return &status.IssueInfo{Key: key, Status: statusName, AssigneeAccountID: assigneeID}
}

func (c *Client) GetTasksStatus(issueKeys []string) map[string]status.IssueInfo {
	out := map[string]status.IssueInfo{}
	if len(issueKeys) == 0 {
		return out
	}
	jql := fmt.Sprintf("issueKey in (%s)", strings.Join(issueKeys, ","))
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", "status,assignee")
	q.Set("maxResults", fmt.Sprintf("%d", len(issueKeys)))
	raw := c.get("search/jql", q)
	if raw == nil {
		return out
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	issues, _ := root["issues"].([]any)
	for _, item := range issues {
		issue, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := issue["key"].(string)
		fields, _ := issue["fields"].(map[string]any)
		var statusName, assigneeID string
		if st, ok := fields["status"].(map[string]any); ok {
			statusName, _ = st["name"].(string)
		}
		if assignee, ok := fields["assignee"].(map[string]any); ok {
			assigneeID, _ = assignee["accountId"].(string)
		}
		out[key] = status.IssueInfo{
			Key:               key,
			Status:            statusName,
			AssigneeAccountID: assigneeID,
		}
	}
	return out
}

func (c *Client) Assign(issueKey, accountID string) bool {
	ok := c.put("issue/"+issueKey+"/assignee", map[string]string{"accountId": accountID})
	if ok {
		slog.Info("jira assigned", "key", issueKey, "accountId", accountID)
	}
	return ok
}

// Transition finds the transition whose target status matches
// case-insensitively, then executes it.
func (c *Client) Transition(issueKey, targetStatus string) bool {
	if targetStatus == "" {
		slog.Error("jira transition skipped: empty target status", "key", issueKey)
		return false
	}
	raw := c.get("issue/"+issueKey+"/transitions", nil)
	if raw == nil {
		return false
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	transitions, _ := root["transitions"].([]any)
	var matchID string
	var available []string
	target := strings.ToLower(targetStatus)
	for _, t := range transitions {
		tr, ok := t.(map[string]any)
		if !ok {
			continue
		}
		to, _ := tr["to"].(map[string]any)
		toName, _ := to["name"].(string)
		available = append(available, toName)
		if strings.ToLower(toName) == target {
			matchID, _ = tr["id"].(string)
		}
	}
	if matchID == "" {
		slog.Error("jira transition target not found", "key", issueKey, "status", targetStatus, "available", available)
		return false
	}
	result := c.post("issue/"+issueKey+"/transitions", map[string]any{"transition": map[string]string{"id": matchID}})
	if result != nil {
		slog.Info("jira transitioned", "key", issueKey, "status", targetStatus)
		return true
	}
	return false
}

// CreateSubtask returns the new issue key, or "" on failure.
func (c *Client) CreateSubtask(projectKey, summary, description, parentKey string) string {
	payload := map[string]any{
		"fields": map[string]any{
			"project":   map[string]string{"key": projectKey},
			"summary":   summary,
			"issuetype": map[string]string{"name": "Subtask"},
			"parent":    map[string]string{"key": parentKey},
			"description": map[string]any{
				"type":    "doc",
				"version": 1,
				"content": textToADF(description),
			},
		},
	}
	raw := c.post("issue", payload)
	root, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	key, _ := root["key"].(string)
	return key
}

// CommentIssue wraps body in an ADF paragraph. Returns false on error.
func (c *Client) CommentIssue(issueKey, body string) bool {
	payload := map[string]any{
		"body": map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": body},
					},
				},
			},
		},
	}
	raw := c.post("issue/"+issueKey+"/comment", payload)
	return raw != nil
}

// CommentIssueCode posts body inside an ADF codeBlock so monospace
// content (e.g. ASCII diagrams) keeps its alignment when rendered.
func (c *Client) CommentIssueCode(issueKey, body string) bool {
	return c.CommentIssueADF(issueKey, []any{
		map[string]any{
			"type": "codeBlock",
			"content": []any{
				map[string]any{"type": "text", "text": body},
			},
		},
	})
}

// CommentIssueADF posts a comment whose body content is the caller-supplied
// ADF node slice. The doc/version envelope is applied here.
func (c *Client) CommentIssueADF(issueKey string, content []any) bool {
	payload := map[string]any{
		"body": map[string]any{
			"type":    "doc",
			"version": 1,
			"content": content,
		},
	}
	raw := c.post("issue/"+issueKey+"/comment", payload)
	return raw != nil
}

func (c *Client) ListSubtasks(parentKey string) []string {
	raw := c.get("issue/"+parentKey, url.Values{"fields": {"subtasks"}})
	root, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	fields, _ := root["fields"].(map[string]any)
	sub, _ := fields["subtasks"].([]any)
	var out []string
	for _, item := range sub {
		st, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if key, _ := st["key"].(string); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// textToADF converts a plain-text description into an ADF content array.
// Blank lines separate blocks. A block whose non-empty lines all start
// with "- " becomes a bulletList; otherwise it becomes a paragraph with
// hardBreak nodes between source lines. An empty input yields a single
// empty paragraph so Jira always receives a valid doc.
func textToADF(text string) []any {
	blocks := splitADFBlocks(text)
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "paragraph"}}
	}
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, renderADFBlock(block))
	}
	return out
}

func splitADFBlocks(text string) [][]string {
	var blocks [][]string
	var current []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				blocks = append(blocks, current)
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

func renderADFBlock(lines []string) map[string]any {
	if isBulletBlock(lines) {
		items := make([]any, 0, len(lines))
		for _, line := range lines {
			body := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(line, " \t"), "-"))
			items = append(items, map[string]any{
				"type": "listItem",
				"content": []any{
					map[string]any{
						"type":    "paragraph",
						"content": []any{map[string]any{"type": "text", "text": body}},
					},
				},
			})
		}
		return map[string]any{"type": "bulletList", "content": items}
	}
	nodes := make([]any, 0, len(lines)*2)
	for i, line := range lines {
		if i > 0 {
			nodes = append(nodes, map[string]any{"type": "hardBreak"})
		}
		nodes = append(nodes, map[string]any{"type": "text", "text": line})
	}
	return map[string]any{"type": "paragraph", "content": nodes}
}

func isBulletBlock(lines []string) bool {
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ") {
			return false
		}
	}
	return len(lines) > 0
}

// FlattenADF returns the concatenated text of an ADF node.
func FlattenADF(node any) string {
	if node == nil {
		return ""
	}
	if s, ok := node.(string); ok {
		return s
	}
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	nodeType, _ := m["type"].(string)
	if nodeType == "text" {
		s, _ := m["text"].(string)
		return s
	}
	children, _ := m["content"].([]any)
	if nodeType == "paragraph" {
		var parts []string
		for _, child := range children {
			parts = append(parts, FlattenADF(child))
		}
		return strings.Join(parts, "")
	}
	var chunks []string
	for _, child := range children {
		if s := FlattenADF(child); s != "" {
			chunks = append(chunks, s)
		}
	}
	return strings.Join(chunks, "\n\n")
}

// ProjectKeyOf returns the prefix: "ENG-101" → "ENG".
func ProjectKeyOf(issueKey string) string {
	if i := strings.IndexByte(issueKey, '-'); i > 0 {
		return issueKey[:i]
	}
	return ""
}
