package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// InsecureWebhooksEnv opts out of HMAC verification when set to "1".
// Intended for local development against `cloudflared` / `ngrok`
// tunnels where there is no shared secret to configure. Production
// deployments must leave it unset and export the per-provider
// *_WEBHOOK_SECRET env vars instead.
const InsecureWebhooksEnv = "VELOCITY_INSECURE_WEBHOOKS"

// verifyHMACSHA256 checks "sha256=<hex>" headers (both GitHub and
// Jira Cloud use this format). Empty secret rejects unless
// VELOCITY_INSECURE_WEBHOOKS=1 is set in the environment, which
// allows operators to run a daemon without a shared secret during
// local development.
func verifyHMACSHA256(secret, header string, body []byte) bool {
	if secret == "" {
		return os.Getenv(InsecureWebhooksEnv) == "1"
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}
