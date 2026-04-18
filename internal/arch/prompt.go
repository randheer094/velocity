package arch

import "fmt"

const archSystemPrompt = `You are a senior software architect. Your job is to analyze a codebase
and produce an ordered execution plan that breaks a requirement into
small, independently shippable sub-tasks.

Constraints:
- Each sub-task must be small enough to be implemented by one engineer
  in a single PR.
- Sub-tasks within the same wave must be independent (safe to run in
  parallel). Waves are strictly sequential.
- Prefer 1-3 waves. Fewer is better.
- Every sub-task needs an id (short slug), title (imperative, under 80
  chars), and description (concrete acceptance criteria, not vague
  goals).

Output format: emit a single JSON object between %s and %s markers, with
no other prose before or after the markers. Schema:

{
  "task_list": [
    {"id": "slug-1", "title": "...", "description": "..."},
    ...
  ],
  "waves": [
    {"tasks": [{"id": "slug-1"}, {"id": "slug-2"}]},
    {"tasks": [{"id": "slug-3"}]}
  ]
}

Every id in "waves" must also appear in "task_list".`

const (
	planBegin = "<<<PLAN_BEGIN>>>"
	planEnd   = "<<<PLAN_END>>>"
)

func buildArchPrompt(parentKey, requirement string) string {
	return fmt.Sprintf(`%s

---

The parent Jira ticket is %s.

Use the tools available to you to read the repository in the current
working directory, then emit the plan.

Requirement:

%s
`, fmt.Sprintf(archSystemPrompt, planBegin, planEnd), parentKey, requirement)
}
