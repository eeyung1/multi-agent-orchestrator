package objective

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// Policy comes from trusted operator configuration, never from a proposal.
type Policy struct {
	AllowedScopes      []string
	Validators         map[string]bool
	Capabilities       map[string]bool
	RequiredValidators []string
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

func Admit(primary string, plan Plan, policy Policy) error {
	if err := Validate(primary, plan); err != nil {
		return err
	}
	if !identifier.MatchString(plan.ID) || len(plan.Tasks) == 0 || len(plan.Tasks) > 100 {
		return fmt.Errorf("a plan needs a safe ID and between 1 and 100 tasks")
	}
	features := map[string]bool{}
	for _, task := range plan.Tasks {
		if !identifier.MatchString(task.ID) || !identifier.MatchString(task.Feature) || features[task.Feature] {
			return fmt.Errorf("task %s requires safe, unique task/feature identifiers", task.ID)
		}
		features[task.Feature] = true
		scope, err := state.CanonicalScope(primary, task.Scope)
		if err != nil || scope != task.Scope {
			return fmt.Errorf("task %s requires a canonical scope", task.ID)
		}
		if reserved(scope) {
			return fmt.Errorf("task %s targets controller/Git metadata", task.ID)
		}
		allowed := false
		for _, parent := range policy.AllowedScopes {
			canonical, err := state.CanonicalScope(primary, parent)
			if err != nil {
				return fmt.Errorf("invalid configured scope: %w", err)
			}
			if canonical == "." || scope == canonical || strings.HasPrefix(scope, canonical+"/") {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("task %s requires a scope expansion decision: %s", task.ID, scope)
		}
		if strings.TrimSpace(task.Description) == "" || len(task.AcceptanceCriteria) == 0 || len(task.Validation) == 0 {
			return fmt.Errorf("task %s requires description, acceptance criteria and validation", task.ID)
		}
		for _, criterion := range task.AcceptanceCriteria {
			if strings.TrimSpace(criterion) == "" {
				return fmt.Errorf("task %s has empty acceptance criteria", task.ID)
			}
		}
		for _, check := range task.Validation {
			if !policy.Validators[check] {
				return fmt.Errorf("task %s requests an unconfigured validator: %s", task.ID, check)
			}
		}
		for _, required := range policy.RequiredValidators {
			found := false
			for _, check := range task.Validation {
				if check == required {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("task %s omits required validator: %s", task.ID, required)
			}
		}
		if !policy.Capabilities[task.Capability] {
			return fmt.Errorf("task %s requests an unavailable worker capability: %s", task.ID, task.Capability)
		}
	}
	// Overlapping tasks must have an explicit dependency ordering. Merely
	// choosing fewer workers must not conceal an ambiguous parallel plan.
	deps := map[string][]string{}
	for _, t := range plan.Tasks {
		deps[t.ID] = t.DependsOn
	}
	var depends func(string, string) bool
	depends = func(a, b string) bool {
		for _, d := range deps[a] {
			if d == b || depends(d, b) {
				return true
			}
		}
		return false
	}
	for i, a := range plan.Tasks {
		for _, b := range plan.Tasks[i+1:] {
			if overlap(a.Scope, b.Scope) && !depends(a.ID, b.ID) && !depends(b.ID, a.ID) {
				return fmt.Errorf("parallel tasks %s and %s overlap", a.ID, b.ID)
			}
		}
	}
	return nil
}

func reserved(scope string) bool {
	for _, part := range strings.Split(scope, "/") {
		if part == ".git" || part == ".agent-worktrees" || part == "multi-agent-orchestrator" || strings.HasPrefix(part, ".agent-") || strings.HasPrefix(part, ".orchestrator-") {
			return true
		}
	}
	// A root claim includes controller metadata and does not bound product work.
	return scope == "."
}
