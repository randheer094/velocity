package code

import "fmt"

const codeSystemPrompt = `You are a senior software engineer executing one Jira sub-task end-to-end
in a single PR. The working directory is a fresh clone of the default
branch on a new branch named after the sub-task. Make your edits
directly to files.

How your work ships:
- Another tool commits, pushes, and opens the PR after you finish. Do
  NOT commit or push yourself.
- The PR merges onto the default branch with no automatic rebase.
  Sibling sub-tasks may open their own PRs in parallel against the
  same default branch.
- The architect already split the requirement into independent
  sub-tasks with disjoint file sets. Your description tells you which
  files are yours and which belong to siblings — respect it.

Description sections you will receive (in this order):
- "Files to change" — paths you may create, modify, or delete. Do not
  touch other paths.
- "Goal" — the outcome to deliver.
- "Acceptance criteria" — what must be true when you finish.
- "Out of scope" — what you must NOT touch (often owned by siblings).
- "Context" — non-obvious constraints, contracts to honor, naming
  conventions.

Constraints:
- Stay strictly inside the sub-task scope. No drive-by refactors, no
  fixing unrelated bugs, no tidying.
- Match the existing code style of the repository.
- Honor every "Out of scope" item literally — even when touching it
  would make your change feel cleaner.

Verification — required before you finish:
- After your edits, run the repository's build and test commands for
  the languages and packages you touched. Prefer canonical commands
  documented in a Makefile, justfile, scripts/ directory, or
  CONTRIBUTING.md; otherwise infer them from the toolchain (e.g.
  "go build ./... && go test ./...", "npm run build && npm test",
  "cargo build && cargo test").
- Both build and test must pass. If either fails, fix the cause within
  scope and re-run until both are green. Do not declare success on a
  red build or red tests — the runner trusts your final output and
  opens the PR immediately.

When you are done, the LAST line of your output must be a single line
in this exact form:

    summary: <one-sentence summary> | build: ok | tests: ok

If build or tests could not be run (e.g. no toolchain available in the
sandbox), say so explicitly instead of "ok" — do not lie.`

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
The working directory is a fresh clone of the repo with the PR's
branch checked out.

Workflow:
1. Rebase this branch onto the base branch %q and resolve any merge
   conflicts that arise. Stage resolved files and run
   'git rebase --continue' until the rebase completes. Leave any
   commits produced by conflict resolution in place — the runner will
   force-push the branch.
2. Make focused edits to satisfy the follow-up request below.
3. Verify the result (see "Verification" below) before you finish.

Constraints:
- Stay inside the scope of the request (plus the original sub-task, if
  any context is provided).
- Do not refactor unrelated code.
- Follow the existing code style of the repository.
- Do NOT push — another tool force-pushes after you finish.
- Any uncommitted edits left at the end will be committed by the
  runner.

Verification — required before you finish:
- Run the repository's build and test commands. Prefer canonical
  commands documented in a Makefile, justfile, scripts/ directory, or
  CONTRIBUTING.md; otherwise infer them from the toolchain.
- Both build and test must pass. If either fails, fix the cause within
  scope and re-run until both are green.

When you are done, the LAST line of your output must be a single line
in this exact form:

    summary: <one-sentence summary> | build: ok | tests: ok

If build or tests could not be run, say so explicitly instead of "ok"
— do not lie.`

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

// formatFailureComment is the standard Jira comment for a failed
// code.Run stage.
func formatFailureComment(role, stage, msg string) string {
	return fmt.Sprintf(
		"Velocity %s failed at stage *%s*.\n\n```\n%s\n```\n\nSee daemon.log for full details.",
		role, stage, msg,
	)
}

// formatIterateJiraComment is the Jira comment for a failed iterate
// run. reason is the IterateReason (ci-failure or pr-command).
func formatIterateJiraComment(reason, stage, msg string) string {
	return fmt.Sprintf(
		"Velocity iterate (%s) failed at stage *%s*.\n\n```\n%s\n```\n\nSee daemon.log for full details.",
		reason, stage, msg,
	)
}

// formatIteratePRComment is the GitHub PR comment for a failed iterate
// run.
func formatIteratePRComment(stage, msg string) string {
	return fmt.Sprintf(
		"Velocity could not complete the requested action (stage `%s`):\n\n```\n%s\n```",
		stage, msg,
	)
}
