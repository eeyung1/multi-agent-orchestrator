package enrollment

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type Disposition string

const (
	Native Disposition = "NATIVE"
	Compatible Disposition = "COMPATIBLE_ADAPTER"
	MigrationRequired Disposition = "MIGRATION_REQUIRED"
	Blocked Disposition = "BLOCKED"
)

type Finding struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Reason string `json:"reason"`
}

type Report struct {
	RepositoryID string `json:"repository_id"`
	Disposition Disposition `json:"disposition"`
	Findings []Finding `json:"findings"`
}

var instructionNames = map[string]bool{
	"AGENTS.md": true, "CLAUDE.md": true, "GEMINI.md": true, "CONTRIBUTING.md": true,
}

var coordinationNames = map[string]bool{
	"agent_coordination.py": true, "agent-coordination.py": true,
	"coordination.py": true, "agent_claims.py": true, "claims.py": true,
}

func Preflight(workspace string) (Report, error) {
	primary, err := repository.Primary(workspace)
	if err != nil { return Report{}, err }
	id, err := repository.Identity(workspace)
	if err != nil { return Report{}, err }

	report := Report{RepositoryID: id, Disposition: Native}
	err = filepath.WalkDir(primary, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil { return walkErr }
		if path == primary { return nil }
		rel, err := filepath.Rel(primary, path)
		if err != nil { return err }
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == ".agent-worktrees" || rel == "multi-agent-orchestrator" ||
				strings.HasPrefix(rel, ".git/") || strings.HasPrefix(rel, ".agent-worktrees/") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if instructionNames[name] {
			hit, err := containsCoordinationDirective(path)
			if err != nil { return err }
			if hit {
				report.Findings = append(report.Findings, Finding{Path: rel, Kind: "instruction", Reason: "contains agent ownership/claim coordination directives"})
			}
		}
		if coordinationNames[name] {
			report.Findings = append(report.Findings, Finding{Path: rel, Kind: "coordination-tool", Reason: "project-local coordination/claim tool requires reconciliation"})
		}
		return nil
	})
	if err != nil { return Report{}, err }
	sort.Slice(report.Findings, func(i,j int) bool { return report.Findings[i].Path < report.Findings[j].Path })
	if len(report.Findings) > 0 { report.Disposition = MigrationRequired }
	return report, nil
}

func containsCoordinationDirective(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil { return false, err }
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.ToLower(s.Text())
		if strings.Contains(line, "claim") && (strings.Contains(line, "agent") || strings.Contains(line, "scope")) {
			return true, nil
		}
		if strings.Contains(line, "orchestrator") && (strings.Contains(line, "do not run") || strings.Contains(line, "never run")) {
			return true, nil
		}
	}
	return false, s.Err()
}

func Enroll(workspace string, disposition Disposition, provenance string) (Report, error) {
	report, err := Preflight(workspace)
	if err != nil { return Report{}, err }
	if strings.TrimSpace(provenance) == "" { return report, fmt.Errorf("enrollment provenance is required") }
	switch disposition {
	case Native:
		if len(report.Findings) != 0 { return report, fmt.Errorf("native enrollment refused: unresolved coordination findings") }
	case Compatible:
		if len(report.Findings) == 0 { return report, fmt.Errorf("compatible adapter enrollment requires a detected coordination mechanism") }
	case MigrationRequired, Blocked:
		return report, fmt.Errorf("%s is not an enrolled/active disposition", disposition)
	default:
		return report, fmt.Errorf("unknown enrollment disposition %q", disposition)
	}

	primary, err := repository.Primary(workspace)
	if err != nil { return report, err }
	store, err := state.Open(primary, report.RepositoryID)
	if err != nil { return report, err }
	err = store.Update("ORCHESTRATOR", "authority-enrollment", func(snap *state.Snapshot) error {
		snap.Authority.Enrollment = string(disposition)
		snap.Authority.Provenance = provenance
		return nil
	})
	if err != nil { return report, err }
	report.Disposition = disposition
	return report, nil
}

func RequireEnrolled(workspace string) error {
	primary, err := repository.Primary(workspace)
	if err != nil { return err }
	id, err := repository.Identity(workspace)
	if err != nil { return err }
	store, err := state.Open(primary, id)
	if err != nil { return err }
	snap, err := store.Read()
	if err != nil { return err }
	if snap.Authority.Enrollment != string(Native) && snap.Authority.Enrollment != string(Compatible) {
		return fmt.Errorf("repository is not authority-enrolled: %s; run preflight and explicit enrollment", snap.Authority.Enrollment)
	}
	return nil
}
