package arch

import (
	"github.com/randheer094/velocity/internal/prompts"
)

const (
	planBegin = "<<<PLAN_BEGIN>>>"
	planEnd   = "<<<PLAN_END>>>"
)

type archPlanData struct {
	PlanBegin   string
	PlanEnd     string
	ParentKey   string
	Requirement string
}

func buildArchPrompt(parentKey, requirement string) (string, error) {
	return prompts.Render("arch_plan", archPlanData{
		PlanBegin:   planBegin,
		PlanEnd:     planEnd,
		ParentKey:   parentKey,
		Requirement: requirement,
	})
}
