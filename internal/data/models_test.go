package data

import "testing"

func TestValidJiraKey(t *testing.T) {
	cases := map[string]bool{
		"ABC-1":     true,
		"PROJ-1234": true,
		"A1B2-9":    true,
		"":          false,
		"abc-1":     false,
		"ABC":       false,
		"ABC-":      false,
		"-1":        false,
		"ABC-01a":   false,
		"1ABC-1":    false,
	}
	for in, want := range cases {
		if got := ValidJiraKey(in); got != want {
			t.Errorf("ValidJiraKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPlanValidate(t *testing.T) {
	good := Plan{
		ParentJiraKey: "PROJ-1",
		TaskList: []PlannedTask{
			{ID: "t1", Title: "do the thing"},
		},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good plan failed: %v", err)
	}

	badKey := good
	badKey.ParentJiraKey = "not-a-key"
	if err := badKey.Validate(); err == nil {
		t.Error("expected error for invalid parent key")
	}

	missingID := Plan{
		ParentJiraKey: "PROJ-1",
		TaskList:      []PlannedTask{{Title: "x"}},
	}
	if err := missingID.Validate(); err == nil {
		t.Error("expected error for missing task id")
	}

	missingTitle := Plan{
		ParentJiraKey: "PROJ-1",
		TaskList:      []PlannedTask{{ID: "t1"}},
	}
	if err := missingTitle.Validate(); err == nil {
		t.Error("expected error for missing task title")
	}
}

func TestCodeTaskValidate(t *testing.T) {
	good := CodeTask{
		IssueKey: "PROJ-2",
		RepoURL:  "https://example.com/repo.git",
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good task failed: %v", err)
	}

	badKey := good
	badKey.IssueKey = "bad"
	if err := badKey.Validate(); err == nil {
		t.Error("expected error for invalid issue key")
	}

	missingRepo := CodeTask{IssueKey: "PROJ-2"}
	if err := missingRepo.Validate(); err == nil {
		t.Error("expected error for missing repo_url")
	}
}
