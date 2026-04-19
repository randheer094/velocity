package config

// Secret env var names. Operators must export these before `velocity start`.
// Webhook secrets are optional — leaving them unset disables HMAC verification.
// DB host + password are required at daemon start; port overrides the
// database.port from config when set; user/db go in config.
const (
	JiraTokenEnv           = "JIRA_API_TOKEN"
	GithubTokenEnv         = "GH_TOKEN"
	JiraWebhookSecretEnv   = "JIRA_WEBHOOK_SECRET"
	GithubWebhookSecretEnv = "GH_WEBHOOK_SECRET"
	DBHostEnv              = "VELOCITY_DB_HOST"
	DBPortEnv              = "VELOCITY_DB_PORT"
	DBPasswordEnv          = "VELOCITY_DB_PASSWORD"
)
