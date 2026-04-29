package code

import (
	"fmt"

	"github.com/randheer094/velocity/internal/prompts"
)

type codeRunData struct {
	IssueKey    string
	Title       string
	Description string
}

type codeIterateData struct {
	IssueKey    string
	Title       string
	Description string
	BaseBranch  string
	Extra       string
}

type failureData struct {
	Role    string
	Stage   string
	Message string
}

type iterateJiraData struct {
	Reason  string
	Stage   string
	Message string
}

type iteratePRData struct {
	Stage   string
	Message string
}

func buildCodePrompt(issueKey, title, description string) (string, error) {
	return prompts.Render("code_run", codeRunData{
		IssueKey:    issueKey,
		Title:       title,
		Description: description,
	})
}

func buildIteratePrompt(issueKey, title, description, baseBranch, extra string) (string, error) {
	return prompts.Render("code_iterate", codeIterateData{
		IssueKey:    issueKey,
		Title:       title,
		Description: description,
		BaseBranch:  baseBranch,
		Extra:       extra,
	})
}

// BuildPRBody returns a GitHub PR description for one sub-task. PR
// bodies stay inline — they're not part of the LLM prompt schema.
func BuildPRBody(title, description, issueKey, jiraURL string) string {
	return fmt.Sprintf(`## %s

%s

---

Jira: [%s](%s)
`, title, description, issueKey, jiraURL)
}
