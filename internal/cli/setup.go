package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
)

func newSetupCmd() *cobra.Command {
	var edit bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive credential + config onboarding",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(edit)
		},
	}
	cmd.Flags().BoolVar(&edit, "edit", false, "Re-prompt even when values already exist")
	return cmd
}

func runSetup(edit bool) error {
	cfg := config.Get()
	existing := cfg
	if existing == nil {
		existing = &config.Config{}
	}

	token, _ := config.GetSecret(config.JiraTokenKey)
	ghToken, _ := config.GetSecret(config.GithubTokenKey)
	jiraSecret, _ := config.GetSecret(config.JiraWebhookSecretKey)
	ghSecret, _ := config.GetSecret(config.GithubWebhookSecretKey)

	if !edit && cfg != nil && token != "" && ghToken != "" {
		fmt.Println("velocity already configured. Re-run with --edit to modify.")
		return nil
	}

	email := existing.Jira.Email
	baseURL := existing.Jira.BaseURL
	archID := existing.Jira.ArchitectJiraID
	devID := existing.Jira.DeveloperJiraID
	repoField := existing.Jira.RepoURLField

	taskNew := bucketToInput(existing.Jira.TaskStatusMap.New, "To Do")
	taskPlanning := bucketToInput(existing.Jira.TaskStatusMap.Planning, "Planning")
	taskPlanFail := bucketToInput(existing.Jira.TaskStatusMap.PlanningFailed, "Planning Failed")
	taskSIP := bucketToInput(existing.Jira.TaskStatusMap.SubtaskInProgress, "In Progress")
	taskDone := bucketToInput(existing.Jira.TaskStatusMap.Done, "Done")
	taskDismissed := bucketToInput(existing.Jira.TaskStatusMap.Dismissed, "Dismissed")

	subNew := bucketToInput(existing.Jira.SubtaskStatusMap.New, "To Do")
	subInProg := bucketToInput(existing.Jira.SubtaskStatusMap.InProgress, "In Progress")
	subPROpen := bucketToInput(existing.Jira.SubtaskStatusMap.PROpen, "In Review")
	subCodeFail := bucketToInput(existing.Jira.SubtaskStatusMap.CodeFailed, "Dev Failed")
	subDone := bucketToInput(existing.Jira.SubtaskStatusMap.Done, "Done")
	subDismissed := bucketToInput(existing.Jira.SubtaskStatusMap.Dismissed, "Dismissed")

	bucketDesc := "Comma-separated Jira status names. The first is the default used when transitioning into this bucket; the rest are aliases that also resolve to it on incoming events."

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Jira email").Value(&email).Validate(nonEmpty),
			huh.NewInput().Title("Jira API token").EchoMode(huh.EchoModePassword).Value(&token).Validate(nonEmpty),
		),
		huh.NewGroup(
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
				Title("Jira webhook secret (optional)").
				Description("HMAC-SHA256 secret Jira signs the body with. Sent as X-Hub-Signature: sha256=<hex>").
				Value(&jiraSecret),
		),
		huh.NewGroup(
			huh.NewNote().Title("Parent task workflow statuses").Description(bucketDesc),
			huh.NewInput().Title("NEW statuses").Value(&taskNew).Validate(nonEmpty),
			huh.NewInput().Title("PLANNING statuses").Value(&taskPlanning).Validate(nonEmpty),
			huh.NewInput().Title("PLANNING_FAILED statuses").Value(&taskPlanFail).Validate(nonEmpty),
			huh.NewInput().Title("SUBTASK_IN_PROGRESS statuses").Value(&taskSIP).Validate(nonEmpty),
			huh.NewInput().Title("DONE statuses").Value(&taskDone).Validate(nonEmpty),
			huh.NewInput().Title("DISMISSED statuses").Value(&taskDismissed).Validate(nonEmpty),
		),
		huh.NewGroup(
			huh.NewNote().Title("Sub-task workflow statuses").Description(bucketDesc),
			huh.NewInput().Title("NEW statuses").Value(&subNew).Validate(nonEmpty),
			huh.NewInput().Title("IN_PROGRESS statuses").Value(&subInProg).Validate(nonEmpty),
			huh.NewInput().Title("PR_OPEN statuses").Value(&subPROpen).Validate(nonEmpty),
			huh.NewInput().Title("CODE_FAILED statuses").Value(&subCodeFail).Validate(nonEmpty),
			huh.NewInput().Title("DONE statuses").Value(&subDone).Validate(nonEmpty),
			huh.NewInput().Title("DISMISSED statuses").Value(&subDismissed).Validate(nonEmpty),
		),
		huh.NewGroup(
			huh.NewNote().Title("GitHub"),
			huh.NewInput().Title("GitHub token (repo scope)").EchoMode(huh.EchoModePassword).Value(&ghToken).Validate(nonEmpty),
			huh.NewInput().Title("GitHub webhook secret (optional, for HMAC verification)").Value(&ghSecret),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	if err := config.SetSecret(config.JiraTokenKey, token); err != nil {
		return fmt.Errorf("save jira token: %w", err)
	}
	if err := config.SetSecret(config.GithubTokenKey, ghToken); err != nil {
		return fmt.Errorf("save github token: %w", err)
	}
	if err := config.SetSecret(config.JiraWebhookSecretKey, jiraSecret); err != nil {
		return fmt.Errorf("save jira webhook secret: %w", err)
	}
	if err := config.SetSecret(config.GithubWebhookSecretKey, ghSecret); err != nil {
		return fmt.Errorf("save github webhook secret: %w", err)
	}

	newCfg := &config.Config{
		Jira: config.JiraConfig{
			BaseURL:         baseURL,
			Email:           email,
			ArchitectJiraID: archID,
			DeveloperJiraID: devID,
			RepoURLField:    repoField,
			TaskStatusMap: config.TaskStatusMap{
				New:               parseBucketInput(taskNew),
				Planning:          parseBucketInput(taskPlanning),
				PlanningFailed:    parseBucketInput(taskPlanFail),
				SubtaskInProgress: parseBucketInput(taskSIP),
				Done:              parseBucketInput(taskDone),
				Dismissed:         parseBucketInput(taskDismissed),
			},
			SubtaskStatusMap: config.SubtaskStatusMap{
				New:        parseBucketInput(subNew),
				InProgress: parseBucketInput(subInProg),
				PROpen:     parseBucketInput(subPROpen),
				CodeFailed: parseBucketInput(subCodeFail),
				Done:       parseBucketInput(subDone),
				Dismissed:  parseBucketInput(subDismissed),
			},
		},
	}
	if err := config.Save(newCfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("velocity configured. Start the server with: velocity start")
	return nil
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("cannot be empty")
	}
	return nil
}

// bucketToInput renders "default, alias1, alias2". Empty → fallback.
func bucketToInput(b config.StatusBucket, fallback string) string {
	names := b.All()
	if len(names) == 0 {
		return fallback
	}
	return strings.Join(names, ", ")
}

// parseBucketInput: first entry is Default, rest are Aliases; blanks dropped.
func parseBucketInput(s string) config.StatusBucket {
	var out config.StatusBucket
	for _, raw := range strings.Split(s, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if out.Default == "" {
			out.Default = name
			continue
		}
		out.Aliases = append(out.Aliases, name)
	}
	return out
}
