// Package planning defines the shared, non-authoritative Planner data contract.
// It has no filesystem, scheduler, registry, provider or transport dependencies.
package planning

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type Existing struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	Status       string `json:"status"`
	HeadSHA      string `json:"head_sha,omitempty"`
	ReviewStatus string `json:"review_status,omitempty"`
}
type Report struct {
	Workspace         string     `json:"workspace"`
	Head              string     `json:"head"`
	Files             []string   `json:"files"`
	Instructions      []File     `json:"instructions"`
	CoordinationFiles []File     `json:"coordination_files"`
	Context           []File     `json:"context"`
	Existing          []Existing `json:"existing_tasks"`
	ClaimedScopes     []string   `json:"claimed_scopes"`
	DirtyPaths        []string   `json:"dirty_paths"`
	Limitations       []string   `json:"limitations"`
	InstructionDigest string     `json:"instruction_digest"`
}

type Task struct {
	ID                 string   `json:"id"`
	Feature            string   `json:"feature"`
	Scope              string   `json:"scope"`
	DependsOn          []string `json:"depends_on,omitempty"`
	Priority           int      `json:"priority,omitempty"`
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Validation         []string `json:"validation,omitempty"`
	Capability         string   `json:"capability,omitempty"`
}

type Plan struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	Tasks     []Task `json:"tasks"`
}

type Request struct {
	Objective          string   `json:"objective"`
	Recon              Report   `json:"recon"`
	Capabilities       []string `json:"capabilities"`
	Validators         []string `json:"validators"`
	AllowedScopes      []string `json:"allowed_scopes"`
	RequiredValidators []string `json:"required_validators,omitempty"`
}
type Proposal struct {
	Plan      Plan     `json:"plan"`
	Questions []string `json:"questions,omitempty"`
}
