package config

import "github.com/zalando/go-keyring"

const (
	KeyringService = "velocity"

	JiraTokenKey          = "JIRA_API_TOKEN"
	GithubTokenKey        = "GITHUB_TOKEN"
	JiraWebhookSecretKey  = "JIRA_WEBHOOK_SECRET"
	GithubWebhookSecretKey = "GITHUB_WEBHOOK_SECRET"
)

func GetSecret(key string) (string, error) {
	return keyring.Get(KeyringService, key)
}

func SetSecret(key, value string) error {
	return keyring.Set(KeyringService, key, value)
}

func DeleteSecret(key string) error {
	return keyring.Delete(KeyringService, key)
}
