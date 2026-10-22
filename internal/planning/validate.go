package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Validate checks wire shape only. Repository scope, dependencies, configured
// capabilities/checks, recon and human authorization are checked by Go admission.
func (p Proposal) Validate() error {
	if len(p.Questions) > 32 {
		return errors.New("too many Planner questions")
	}
	for _, q := range p.Questions {
		if strings.TrimSpace(q) == "" || len(q) > 4096 {
			return errors.New("invalid Planner question")
		}
	}
	if len(p.Questions) > 0 {
		return nil
	}
	if strings.TrimSpace(p.Plan.Objective) == "" || len(p.Plan.Tasks) == 0 || len(p.Plan.Tasks) > 512 {
		return errors.New("invalid Planner proposal")
	}
	seen := map[string]bool{}
	for _, t := range p.Plan.Tasks {
		if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Feature) == "" || strings.TrimSpace(t.Scope) == "" || seen[t.ID] {
			return errors.New("invalid Planner task")
		}
		seen[t.ID] = true
	}
	return nil
}

// PlanDigest omits only the model-suggested objective ID, which Go replaces.
// The complete task plan and exact objective are bound to the response evidence.
func PlanDigest(p Plan) string {
	p.ID = ""
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
