package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// verifyHMACSHA256 checks "sha256=<hex>" headers (both GitHub and Jira
// Cloud use this format). Empty secret → accept (dev only).
func verifyHMACSHA256(secret, header string, body []byte) bool {
	if secret == "" {
		return true
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
