package code

import "fmt"

const codeSystemPrompt = `You are a senior software engineer. You are making focused changes in a
git repository to satisfy a single Jira sub-task. The working directory
is the repo root and is already on a fresh branch — make your edits
directly to files.

Constraints:
- Stay inside the scope of the sub-task. Do not refactor unrelated code.
- Follow the existing code style of the repository.
- Do not commit or push — another tool will do that after you finish.
- When you are done, print a short summary of what you changed on the
  last line of your output.`

func buildCodePrompt(issueKey, title, description string) string {
	return fmt.Sprintf(`%s

---

Sub-task: %s
Title: %s

Description:
%s
`, codeSystemPrompt, issueKey, title, description)
}

const iterateSystemPrompt = `You are a senior software engineer iterating on an open pull request.
The working directory is the repo root, the PR's branch is checked out,
and it has just been rebased onto the default branch. Make focused
edits to satisfy the request below.

Constraints:
- Stay inside the scope of the original sub-task plus the request.
- Do not refactor unrelated code.
- Follow the existing code style of the repository.
- Do not commit or push — another tool will do that after you finish.
- When you are done, print a short summary of what you changed on the
  last line of your output.`

func buildIteratePrompt(issueKey, title, description, extra string) string {
	return fmt.Sprintf(`%s

---

Sub-task: %s
Title: %s

Original description:
%s

Follow-up request:
%s
`, iterateSystemPrompt, issueKey, title, description, extra)
}

// BuildPRBody returns a GitHub PR description for one sub-task.
func BuildPRBody(title, description, issueKey, jiraURL string) string {
	return fmt.Sprintf(`## %s

%s

---

Jira: [%s](%s)
`, title, description, issueKey, jiraURL)
}
