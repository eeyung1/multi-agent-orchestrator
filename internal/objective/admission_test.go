package objective

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdmissionRejectsUnsafeAndParallelPlans(t *testing.T) {
	root := t.TempDir()
	policy := Policy{AllowedScopes: []string{"src", "tests"}, Validators: map[string]bool{"check": true}, Capabilities: map[string]bool{"coding": true}}
	makeTask := func(id, scope string) Task {
		return Task{ID: id, Feature: id, Scope: scope, Description: "Implement outcome", AcceptanceCriteria: []string{"Tests pass"}, Validation: []string{"check"}, Capability: "coding"}
	}
	for _, scenario := range []string{"duplicate", "unknown", "cycle", "overlap", "traversal", "absolute", "metadata", "unauthorized", "validator", "capability", "symlink", "noncanonical"} {
		t.Run(scenario, func(t *testing.T) {
			p := Plan{ID: "plan", Objective: "work", Tasks: []Task{makeTask("a", "src/a"), makeTask("b", "tests/b")}}
			switch scenario {
			case "duplicate":
				p.Tasks[1].ID = "a"
			case "unknown":
				p.Tasks[1].DependsOn = []string{"missing"}
			case "cycle":
				p.Tasks[0].DependsOn = []string{"b"}
				p.Tasks[1].DependsOn = []string{"a"}
			case "overlap":
				p.Tasks[1].Scope = "src/a/child"
			case "traversal":
				p.Tasks[0].Scope = "../escape"
			case "absolute":
				p.Tasks[0].Scope = filepath.Join(root, "src")
			case "metadata":
				p.Tasks[0].Scope = ".git/config"
			case "unauthorized":
				p.Tasks[0].Scope = "private"
			case "validator":
				p.Tasks[0].Validation = []string{"shell-command-from-model"}
			case "capability":
				p.Tasks[0].Capability = "admin"
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
				p.Tasks[0].Scope = "alias/file"
			case "noncanonical":
				p.Tasks[0].Scope = "src/../src/a"
			}
			if err := Admit(root, p, policy); err == nil {
				t.Fatalf("accepted %s", scenario)
			}
		})
	}
	p := Plan{ID: "plan", Objective: "work", Tasks: []Task{makeTask("a", "src"), makeTask("b", "src/a")}}
	p.Tasks[1].DependsOn = []string{"a"}
	if err := Admit(root, p, policy); err != nil {
		t.Fatal("ordered overlap should be schedulable:", err)
	}
	policy.Validators["required"] = true
	policy.RequiredValidators = []string{"required"}
	if err := Admit(root, p, policy); err == nil {
		t.Fatal("required repository checks were omitted")
	}
}
