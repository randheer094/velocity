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
The working directory is the repo root on a fresh clone, with the PR's
branch checked out.

Workflow:
1. First, rebase this branch onto the base branch %q and resolve any
   merge conflicts that arise. Stage resolved files and run
   'git rebase --continue' until the rebase completes. Leave any
   commits produced by conflict resolution in place — the runner will
   force-push the branch.
2. Then make focused edits to satisfy the follow-up request below.

Constraints:
- Stay inside the scope of the request (plus the original sub-task, if
  any context is provided).
- Do not refactor unrelated code.
- Follow the existing code style of the repository.
- Do not push — another tool force-pushes after you finish.
- Any uncommitted edits left at the end will be committed by the runner.
- When you are done, print a short summary of what you changed on the
  last line of your output.`

func buildIteratePrompt(issueKey, title, description, baseBranch, extra string) string {
	return fmt.Sprintf(`%s

---

Sub-task: %s
Title: %s

Original description:
%s

Follow-up request:
%s
`, fmt.Sprintf(iterateSystemPrompt, baseBranch), issueKey, title, description, extra)
}

// BuildPRBody returns a GitHub PR description for one sub-task.
func BuildPRBody(title, description, issueKey, jiraURL string) string {
	return fmt.Sprintf(`## %s

%s

---

Jira: [%s](%s)
`, title, description, issueKey, jiraURL)
}
