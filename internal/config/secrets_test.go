package config

import "testing"

func TestSecretWrappers(t *testing.T) {
	// Keyring may not be available in CI; we just exercise the wrapper.
	_, _ = GetSecret("velocity-test-key-not-set")
	_ = SetSecret("velocity-test-key", "v")
	_ = DeleteSecret("velocity-test-key")
}
