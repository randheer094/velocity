package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestJiraHandlerBadSignature(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	t.Setenv(config.JiraWebhookSecretEnv, "shh")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader([]byte(`{"issue":{"key":"X-1"}}`)))
	req.Header.Set("X-Hub-Signature", "sha256=deadbeef")
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestJiraHandlerGoodSignature(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	t.Setenv(config.JiraWebhookSecretEnv, "shh")

	body := []byte(`{"issue":{"key":"X-1","fields":{}}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", sign("shh", body))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestGithubHandlerBadSignature(t *testing.T) {
	t.Setenv(config.GithubWebhookSecretEnv, "shh")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestVerifyHMACSHA256(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	secret := "topsecret"
	good := sign(secret, body)

	if !verifyHMACSHA256(secret, good, body) {
		t.Errorf("good signature should pass")
	}
	if verifyHMACSHA256(secret, good, []byte("tampered")) {
		t.Errorf("tampered body should fail")
	}
	if verifyHMACSHA256(secret, "sha256=deadbeef", body) {
		t.Errorf("wrong signature should fail")
	}
	if verifyHMACSHA256(secret, "no-prefix", body) {
		t.Errorf("missing prefix should fail")
	}
	if verifyHMACSHA256(secret, "sha256=zzz", body) {
		t.Errorf("non-hex should fail")
	}
	if !verifyHMACSHA256("", "anything", body) {
		t.Errorf("empty secret should accept")
	}
}
