package config

// Secret env var names. Operators must export these before `velocity start`.
// Webhook secrets are optional — leaving them unset disables HMAC verification.
// All DB connection fields are required at daemon start. sslmode is always
// `disable`; put a TLS-terminating proxy in front of Postgres if you need
// encryption.
const (
	JiraTokenEnv           = "JIRA_API_TOKEN"
	GithubTokenEnv         = "GH_TOKEN"
	JiraWebhookSecretEnv   = "JIRA_WEBHOOK_SECRET"
	GithubWebhookSecretEnv = "GH_WEBHOOK_SECRET"
	DBHostEnv              = "VELOCITY_DB_HOST"
	DBPortEnv              = "VELOCITY_DB_PORT"
	DBUserEnv              = "VELOCITY_DB_USER"
	DBPasswordEnv          = "VELOCITY_DB_PASSWORD"
	DBNameEnv              = "VELOCITY_DB_NAME"
)
