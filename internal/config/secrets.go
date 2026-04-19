package config

// Secret env var names. Operators must export these before `velocity start`.
// Webhook secrets are optional — leaving them unset disables HMAC verification.
const (
	JiraTokenEnv           = "JIRA_API_TOKEN"
	GithubTokenEnv         = "GH_TOKEN"
	JiraWebhookSecretEnv   = "JIRA_WEBHOOK_SECRET"
	GithubWebhookSecretEnv = "GH_WEBHOOK_SECRET"
)
