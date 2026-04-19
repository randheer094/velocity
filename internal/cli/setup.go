package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/jira"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive config onboarding (secrets come from env vars)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup()
		},
	}
}

func runSetup() error {
	token := os.Getenv(config.JiraTokenEnv)
	if token == "" {
		return fmt.Errorf("%s must be exported before `velocity setup` — setup fetches Jira statuses", config.JiraTokenEnv)
	}

	existing := config.Get()
	if existing == nil {
		existing = &config.Config{}
	}

	email := existing.Jira.Email
	baseURL := existing.Jira.BaseURL
	archID := existing.Jira.ArchitectJiraID
	devID := existing.Jira.DeveloperJiraID
	repoField := existing.Jira.RepoURLField
	projectKeysRaw := strings.Join(existing.Jira.ProjectKeys, ", ")

	connForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Jira email").Value(&email).Validate(nonEmpty),
			huh.NewInput().Title("Jira base URL").Placeholder("https://your-org.atlassian.net").Value(&baseURL).Validate(nonEmpty),
			huh.NewInput().Title("Architect Jira accountId").Value(&archID).Validate(nonEmpty),
			huh.NewInput().Title("Developer Jira accountId").Value(&devID).Validate(nonEmpty),
			huh.NewInput().
				Title("Jira field carrying the GitHub repo URL").
				Description("The custom field ID or name on the parent ticket, e.g. customfield_10050").
				Placeholder("customfield_10050").
				Value(&repoField).
				Validate(nonEmpty),
			huh.NewInput().
				Title("Jira project keys").
				Description("comma-separated — used to fetch the workflow status vocabulary").
				Value(&projectKeysRaw).
				Validate(commaList),
		),
	)
	if err := connForm.Run(); err != nil {
		return err
	}

	projectKeys := splitCommas(projectKeysRaw)

	fmt.Printf("Fetching statuses from Jira for %s...\n", strings.Join(projectKeys, ", "))
	jc := jira.NewWithCreds(baseURL, email, token)
	all := fetchUnionStatuses(jc, projectKeys)
	if len(all) == 0 {
		return fmt.Errorf("no statuses fetched — verify base URL, %s, and project keys, then re-run setup", config.JiraTokenEnv)
	}

	taskMap, err := pickTaskBuckets(all, existing.Jira.TaskStatusMap)
	if err != nil {
		return err
	}
	subMap, err := pickSubtaskBuckets(all, existing.Jira.SubtaskStatusMap)
	if err != nil {
		return err
	}

	newCfg := &config.Config{
		Jira: config.JiraConfig{
			BaseURL:          baseURL,
			Email:            email,
			ArchitectJiraID:  archID,
			DeveloperJiraID:  devID,
			RepoURLField:     repoField,
			ProjectKeys:      projectKeys,
			TaskStatusMap:    taskMap,
			SubtaskStatusMap: subMap,
		},
	}
	if err := config.Save(newCfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("velocity configured.")
	printSecretReminder()
	return nil
}

// pickTaskBuckets renders one MultiSelect + default Select per parent-
// task bucket. Exclusion is per-workflow: options reload when any
// earlier bucket's selection changes via the *[]any binding idiom.
func pickTaskBuckets(all []jira.ProjectStatus, existing config.TaskStatusMap) (config.TaskStatusMap, error) {
	newList, newDef := seedBucket(existing.New, all, "new")
	planList, planDef := seedBucket(existing.Planning, nil, "")
	planFailList, planFailDef := seedBucket(existing.PlanningFailed, nil, "")
	sipList, sipDef := seedBucket(existing.SubtaskInProgress, nil, "")
	doneList, doneDef := seedBucket(existing.Done, all, "done")
	dismList, dismDef := seedBucket(existing.Dismissed, nil, "")

	planDeps := []any{&newList}
	planFailDeps := []any{&newList, &planList}
	sipDeps := []any{&newList, &planList, &planFailList}
	doneDeps := []any{&newList, &planList, &planFailList, &sipList}
	dismDeps := []any{&newList, &planList, &planFailList, &sipList, &doneList}

	form := huh.NewForm(
		huh.NewGroup(huh.NewNote().
			Title("Parent task workflow").
			Description("For each bucket: pick the Jira statuses that belong in it, then set one as the default. Defaults are used when transitioning into the bucket; other selections resolve into the bucket on incoming events.")),
		bucketGroup("NEW", func() []huh.Option[string] { return options(all) }, &newList, &newList, &newDef),
		bucketGroup("PLANNING", func() []huh.Option[string] { return remainingOptions(all, newList) }, &planDeps, &planList, &planDef),
		bucketGroup("PLANNING_FAILED", func() []huh.Option[string] { return remainingOptions(all, newList, planList) }, &planFailDeps, &planFailList, &planFailDef),
		bucketGroup("SUBTASK_IN_PROGRESS", func() []huh.Option[string] { return remainingOptions(all, newList, planList, planFailList) }, &sipDeps, &sipList, &sipDef),
		bucketGroup("DONE", func() []huh.Option[string] { return remainingOptions(all, newList, planList, planFailList, sipList) }, &doneDeps, &doneList, &doneDef),
		bucketGroup("DISMISSED", func() []huh.Option[string] { return remainingOptions(all, newList, planList, planFailList, sipList, doneList) }, &dismDeps, &dismList, &dismDef),
	)
	if err := form.Run(); err != nil {
		return config.TaskStatusMap{}, err
	}

	return config.TaskStatusMap{
		New:               toBucket(newDef, newList),
		Planning:          toBucket(planDef, planList),
		PlanningFailed:    toBucket(planFailDef, planFailList),
		SubtaskInProgress: toBucket(sipDef, sipList),
		Done:              toBucket(doneDef, doneList),
		Dismissed:         toBucket(dismDef, dismList),
	}, nil
}

func pickSubtaskBuckets(all []jira.ProjectStatus, existing config.SubtaskStatusMap) (config.SubtaskStatusMap, error) {
	newList, newDef := seedBucket(existing.New, all, "new")
	ipList, ipDef := seedBucket(existing.InProgress, nil, "")
	prList, prDef := seedBucket(existing.PROpen, nil, "")
	cfList, cfDef := seedBucket(existing.CodeFailed, nil, "")
	doneList, doneDef := seedBucket(existing.Done, all, "done")
	dismList, dismDef := seedBucket(existing.Dismissed, nil, "")

	ipDeps := []any{&newList}
	prDeps := []any{&newList, &ipList}
	cfDeps := []any{&newList, &ipList, &prList}
	doneDeps := []any{&newList, &ipList, &prList, &cfList}
	dismDeps := []any{&newList, &ipList, &prList, &cfList, &doneList}

	form := huh.NewForm(
		huh.NewGroup(huh.NewNote().
			Title("Sub-task workflow").
			Description("Independent of the parent workflow — a status can appear in both workflows, but not in two buckets of this one.")),
		bucketGroup("NEW", func() []huh.Option[string] { return options(all) }, &newList, &newList, &newDef),
		bucketGroup("IN_PROGRESS", func() []huh.Option[string] { return remainingOptions(all, newList) }, &ipDeps, &ipList, &ipDef),
		bucketGroup("PR_OPEN", func() []huh.Option[string] { return remainingOptions(all, newList, ipList) }, &prDeps, &prList, &prDef),
		bucketGroup("CODE_FAILED", func() []huh.Option[string] { return remainingOptions(all, newList, ipList, prList) }, &cfDeps, &cfList, &cfDef),
		bucketGroup("DONE", func() []huh.Option[string] { return remainingOptions(all, newList, ipList, prList, cfList) }, &doneDeps, &doneList, &doneDef),
		bucketGroup("DISMISSED", func() []huh.Option[string] { return remainingOptions(all, newList, ipList, prList, cfList, doneList) }, &dismDeps, &dismList, &dismDef),
	)
	if err := form.Run(); err != nil {
		return config.SubtaskStatusMap{}, err
	}

	return config.SubtaskStatusMap{
		New:        toBucket(newDef, newList),
		InProgress: toBucket(ipDef, ipList),
		PROpen:     toBucket(prDef, prList),
		CodeFailed: toBucket(cfDef, cfList),
		Done:       toBucket(doneDef, doneList),
		Dismissed:  toBucket(dismDef, dismList),
	}, nil
}

// bucketGroup renders a MultiSelect + default Select pair. `optsFn` is
// the live options producer; `multiDep` is the huh binding that triggers
// OptionsFunc re-evaluation (pass &list itself for the first bucket).
func bucketGroup(label string, optsFn func() []huh.Option[string], multiDep any, list *[]string, def *string) *huh.Group {
	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title(label+" statuses").
			OptionsFunc(optsFn, multiDep).
			Value(list).
			Validate(nonEmptySlice),
		huh.NewSelect[string]().
			Title("Default "+label+" status").
			OptionsFunc(func() []huh.Option[string] { return stringOptions(*list) }, list).
			Value(def).
			Validate(nonEmpty),
	)
}

// seedBucket returns (list, default) from the existing bucket if set,
// otherwise seeds from Jira's statusCategory when `category` is non-
// empty. When no seed is available, returns empty values.
func seedBucket(existing config.StatusBucket, all []jira.ProjectStatus, category string) ([]string, string) {
	if existing.Default != "" {
		return existing.All(), existing.Default
	}
	if category == "" || all == nil {
		return nil, ""
	}
	list := statusNamesByCategory(all, category)
	var def string
	if len(list) > 0 {
		def = list[0]
	}
	return list, def
}

func toBucket(def string, list []string) config.StatusBucket {
	out := config.StatusBucket{Default: def}
	for _, name := range list {
		if name == def {
			continue
		}
		out.Aliases = append(out.Aliases, name)
	}
	return out
}

// fetchUnionStatuses unions statuses across every configured project,
// preserving first-seen order so the multi-select matches Jira's order.
func fetchUnionStatuses(c *jira.Client, projectKeys []string) []jira.ProjectStatus {
	seen := map[string]string{}
	var order []string
	for _, pk := range projectKeys {
		for _, ps := range c.GetProjectStatuses(pk) {
			if _, dup := seen[ps.Name]; dup {
				continue
			}
			seen[ps.Name] = ps.Category
			order = append(order, ps.Name)
		}
	}
	out := make([]jira.ProjectStatus, 0, len(order))
	for _, n := range order {
		out = append(out, jira.ProjectStatus{Name: n, Category: seen[n]})
	}
	return out
}

func statusNamesByCategory(all []jira.ProjectStatus, category string) []string {
	var out []string
	for _, ps := range all {
		if ps.Category == category {
			out = append(out, ps.Name)
		}
	}
	return out
}

func options(all []jira.ProjectStatus) []huh.Option[string] {
	out := make([]huh.Option[string], 0, len(all))
	for _, ps := range all {
		out = append(out, huh.NewOption(ps.Name, ps.Name))
	}
	return out
}

func remainingOptions(all []jira.ProjectStatus, used ...[]string) []huh.Option[string] {
	taken := map[string]bool{}
	for _, u := range used {
		for _, s := range u {
			taken[strings.ToLower(s)] = true
		}
	}
	out := make([]huh.Option[string], 0, len(all))
	for _, ps := range all {
		if taken[strings.ToLower(ps.Name)] {
			continue
		}
		out = append(out, huh.NewOption(ps.Name, ps.Name))
	}
	return out
}

func stringOptions(values []string) []huh.Option[string] {
	out := make([]huh.Option[string], 0, len(values))
	for _, v := range values {
		out = append(out, huh.NewOption(v, v))
	}
	return out
}

func printSecretReminder() {
	fmt.Println()
	fmt.Println("Export these env vars before `velocity start`:")
	fmt.Printf("  %s            (required)\n", config.JiraTokenEnv)
	fmt.Printf("  %s                 (required)\n", config.GithubTokenEnv)
	fmt.Printf("  %s    (optional — HMAC verification)\n", config.JiraWebhookSecretEnv)
	fmt.Printf("  %s      (optional — HMAC verification)\n", config.GithubWebhookSecretEnv)
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("cannot be empty")
	}
	return nil
}

func nonEmptySlice(v []string) error {
	if len(v) == 0 {
		return fmt.Errorf("select at least one")
	}
	return nil
}

func commaList(s string) error {
	if len(splitCommas(s)) == 0 {
		return fmt.Errorf("at least one value required")
	}
	return nil
}

func splitCommas(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
