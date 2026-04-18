package github

// SetAPIBaseForTest swaps the GitHub API base URL. Tests in other
// packages use it to redirect github.New() at an httptest server.
func SetAPIBaseForTest(s string) { apiBase = s }
