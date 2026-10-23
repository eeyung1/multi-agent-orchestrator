package operations

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

func IsCommand(command string) bool {
	switch command {
	case "status", "inspect-agent", "inspect-task", "candidates", "recover-task", "disconnect-agent", "operations-help":
		return true
	}
	return false
}

func Run(ctx context.Context, args []string, out io.Writer) error {
	return RunWithOptions(ctx, args, out, Options{})
}

// Console adds only read commands to existing trusted admission/role consoles.
// Mutating operations remain explicit CLI actions or the existing host prompts.
func Console(ctx context.Context, workspace, line string, out io.Writer, opt Options) (bool, error) {
	args := strings.Fields(line)
	if len(args) == 0 {
		return false, nil
	}
	if args[0] != "status" && args[0] != "inspect-agent" && args[0] != "inspect-task" {
		return false, nil
	}
	args = append(args, "--workspace", workspace)
	return true, RunWithOptions(ctx, args, out, opt)
}

func RunWithOptions(ctx context.Context, args []string, out io.Writer, opt Options) error {
	if len(args) == 0 || !IsCommand(args[0]) {
		return errors.New("unknown operations command")
	}
	command := args[0]
	if command == "operations-help" {
		_, err := fmt.Fprintln(out, "Operations: status [--json] [--watch --interval 2s --updates N], inspect-agent --agent ID [--session ID], inspect-task --objective ID --task ID, candidates --role PLANNER|WORKER, recover-task, disconnect-agent. Use --workspace PATH. Recovery/disconnect require --yes --actor HUMAN --reason TEXT and exact generation/session identity; recovery also requires --writers-stopped. See docs/operations.md.")
		return err
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	workspace := f.String("workspace", ".", "local workspace")
	jsonMode := f.Bool("json", false, "structured operations/v1 output")
	watchMode := f.Bool("watch", false, "read-only cancellable refresh (status only)")
	interval := f.Duration("interval", 2*time.Second, "watch refresh interval, 250ms to 1h")
	updates := f.Int("updates", 0, "stop watch after N snapshots; zero means until cancellation")
	config := f.String("config", "", "optional trusted policy; reads only max_workers for display")
	agent := f.String("agent", "", "logical agent ID")
	session := f.String("session", "", "exact enrolled session ID")
	incarnation := f.Uint64("incarnation", 0, "exact enrolled incarnation")
	objective := f.String("objective", "", "objective ID")
	task := f.String("task", "", "task ID")
	generation := f.Uint64("generation", 0, "exact assignment generation")
	role := f.String("role", "", "candidate role")
	yes := f.Bool("yes", false, "explicit human authorization")
	actor := f.String("actor", "", "human actor recorded by existing APIs")
	reason := f.String("reason", "", "human reason recorded by existing APIs")
	writersStopped := f.Bool("writers-stopped", false, "human confirms old external writers stopped")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprintln(out, "Use orchestrator operations-help and docs/operations.md for operator commands.")
			return err
		}
		return errors.New("invalid operations arguments")
	}
	if f.NArg() != 0 || *interval < 250*time.Millisecond || *interval > time.Hour || *updates < 0 || *updates > 10000 || (*watchMode && command != "status") || (!*watchMode && *updates != 0) {
		return errors.New("invalid operations arguments or watch bounds")
	}
	if *config != "" {
		file, err := os.Open(*config)
		if err != nil {
			return errors.New("display policy unavailable")
		}
		defer file.Close()
		if info, e := file.Stat(); e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("invalid bounded display policy")
		}
		b, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		var fields map[string]json.RawMessage
		if e != nil || len(b) > 1<<20 || strictjson.Decode(b, &fields) != nil || fields == nil {
			return errors.New("invalid display policy")
		}
		max, found := 0, false
		for k, value := range fields {
			if strings.EqualFold(k, "max_workers") {
				if found || strictjson.Decode(value, &max) != nil {
					return errors.New("invalid display policy")
				}
				found = true
			}
		}
		if max == 0 {
			max = 3
		}
		opt.MaxWorkers = &max
		opt.ConfigurationSource = "provided_config_not_authority_verified"
	}
	r, err := Open(*workspace, opt)
	if err != nil {
		return err
	}
	if *watchMode {
		return watch(ctx, r.Capture, out, *jsonMode, *interval, *updates, nil)
	}
	v, err := r.Capture(ctx)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch command {
	case "status":
		if *jsonMode {
			return WriteJSON(out, v)
		}
		return WriteHuman(out, v)
	case "inspect-agent":
		if *agent == "" {
			return errors.New("inspect-agent requires --agent")
		}
		found := []Agent{}
		plans := []Planner{}
		attention := []Attention{}
		for _, a := range v.Agents {
			if string(a.AgentID) == *agent && (*session == "" || string(a.SessionID) == *session) {
				found = append(found, a)
				for _, p := range append(append([]Planner{}, v.Planners...), v.PlannerHistory...) {
					if p.SessionID == a.SessionID {
						plans = append(plans, p)
					}
				}
				for _, item := range v.Attention {
					if item.SessionID == string(a.SessionID) {
						attention = append(attention, item)
					}
				}
			}
		}
		if len(found) == 0 {
			return errors.New("enrolled agent/session not found")
		}
		tasks := []Task{}
		for _, t := range v.Tasks {
			if t.Owner != nil && string(t.Owner.AgentID) == *agent && (*session == "" || string(t.Owner.SessionID) == *session) {
				tasks = append(tasks, t)
			}
		}
		inspection := struct {
			Version   string      `json:"version"`
			Revision  uint64      `json:"revision"`
			Agents    []Agent     `json:"agents"`
			Planners  []Planner   `json:"planners"`
			Tasks     []Task      `json:"tasks"`
			Attention []Attention `json:"attention"`
		}{Version, v.Revision, found, plans, tasks, attention}
		if *jsonMode {
			return WriteJSON(out, inspection)
		}
		v.Agents, v.Planners, v.Tasks, v.Attention = found, plans, tasks, attention
		if err := WriteHuman(out, v); err != nil {
			return err
		}
		for _, a := range found {
			if _, err := fmt.Fprintf(out, "Agent inspection: %q/%q incarnation=%d heartbeat=%s role_decision=%q reason=%q\n", a.AgentID, a.SessionID, a.Incarnation, a.LastHeartbeat.Format(time.RFC3339Nano), a.RoleDecision, a.StateReason); err != nil {
				return err
			}
			for _, h := range a.History {
				if _, err := fmt.Fprintf(out, "History: %q/%q gen=%d branch=%q worktree=%q blocker=%s\n", h.Assignment.ObjectiveID, h.Assignment.TaskID, h.Assignment.Generation, h.Branch, h.Worktree, quotedSummary(h.Blocker)); err != nil {
					return err
				}
			}
		}
		return nil
	case "inspect-task":
		for _, t := range v.Tasks {
			if t.Assignment.ObjectiveID == *objective && t.Assignment.TaskID == *task {
				if *jsonMode {
					return WriteJSON(out, struct {
						Version  string `json:"version"`
						Revision uint64 `json:"revision"`
						Task     Task   `json:"task"`
					}{Version, v.Revision, t})
				}
				attention := vAttentionForTask(v, t)
				v.Tasks = []Task{t}
				v.Agents = nil
				v.Planners = nil
				v.Attention = attention
				if err := WriteHuman(out, v); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(out, "Description: %s\nScope: %q\nDependencies: %s\nCapability: %q\nValidation: %s checks=%s head=%q\nReview: %s\nRecovery: %s\nHistory entries: %d\n", quotedSummary(t.Description), t.Scope, quotedList(t.Dependencies), t.Capability, t.ValidationStatus, quotedList(t.Validators), t.ValidatedHead, t.ReviewStatus, t.Recovery, len(t.History)); err != nil {
					return err
				}
				if t.Result != nil {
					if _, err := fmt.Fprintf(out, "Result: %q head=%q evidence_present=%t artifacts=%s\n", t.Result.ID, t.Result.Head, t.Result.EvidencePresent, quotedList(t.Result.Artifacts)); err != nil {
						return err
					}
				}
				for _, h := range t.History {
					if _, err := fmt.Fprintf(out, "History: gen=%d owner=%q/%q mode=%s branch=%q worktree=%q blocker=%s\n", h.Assignment.Generation, h.Owner.AgentID, h.Owner.SessionID, h.Owner.Mode, h.Branch, h.Worktree, quotedSummary(h.Blocker)); err != nil {
						return err
					}
				}
				return nil
			}
		}
		return errors.New("objective/task not found; provide --objective and --task")
	case "candidates":
		if *role != "PLANNER" && *role != "WORKER" {
			return errors.New("candidates requires --role PLANNER or WORKER")
		}
		candidates := []Agent{}
		for _, a := range v.Agents {
			if (*role == "PLANNER" && a.PlannerCandidate) || (*role == "WORKER" && a.WorkerCandidate) {
				candidates = append(candidates, a)
			}
		}
		if *jsonMode {
			return WriteJSON(out, struct {
				Version    string  `json:"version"`
				Revision   uint64  `json:"revision"`
				Candidates []Agent `json:"candidates"`
			}{Version, v.Revision, candidates})
		}
		v.Agents = candidates
		v.Tasks = nil
		v.Planners = nil
		v.Attention = nil
		return WriteHuman(out, v)
	case "recover-task", "disconnect-agent":
		if !*yes || state.RegistryDecision(*actor) != nil || state.RegistryDecision(*reason) != nil || *actor == "ORCHESTRATOR" {
			return errors.New("explicit --yes, nonempty --actor HUMAN, and --reason are required")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		decision := "operator " + *actor + ": " + *reason
		if state.RegistryDecision(decision) != nil {
			return errors.New("invalid operator attribution")
		}
		if command == "recover-task" {
			if !*writersStopped || *generation == 0 {
				return errors.New("recovery requires --writers-stopped and exact --generation after inspection")
			}
			allowed := false
			for _, t := range v.Tasks {
				if t.Assignment.ObjectiveID == *objective && t.Assignment.TaskID == *task && t.Assignment.Generation == *generation && t.Recovery == "EXPLICIT_RECOVERY_AVAILABLE" {
					allowed = true
				}
			}
			if !allowed {
				return errors.New("assignment is not eligible for this recovery action; inspect exact state/generation")
			}
			err = scheduler.AuthorizeRecovery(r.primary, *objective, *task, *generation, decision)
		} else {
			ref := state.AgentRef{AgentID: agentprotocol.AgentID(*agent), SessionID: agentprotocol.SessionID(*session), Generation: state.AgentSessionGeneration(*incarnation)}
			if ref.Validate() != nil {
				return errors.New("disconnect requires exact --agent, --session and --incarnation")
			}
			s, e := state.ReadOnly(r.primary, r.repositoryID)
			if e != nil || s.AgentRegistry == nil {
				return ErrAuthority
			}
			registry, e := agentregistry.Open(r.primary, agentregistry.Policy{StaleAfter: s.AgentRegistry.StaleAfterNS}, nil)
			if e != nil {
				return ErrAuthority
			}
			err = registry.Disconnect(ref, decision)
		}
		if err != nil {
			return errors.New("authoritative operator action rejected; inspect current binding, generation, and recovery state")
		}
		if *jsonMode {
			return WriteJSON(out, struct {
				Version   string `json:"version"`
				Operation string `json:"operation"`
				Accepted  bool   `json:"accepted"`
			}{Version, command, true})
		}
		_, err = fmt.Fprintln(out, "Operator action accepted by existing Go authority; inspect status/audit for changes. Ownership is never transferred by this output.")
		return err
	}
	return errors.New("unsupported operations command")
}

func vAttentionForTask(v Snapshot, t Task) []Attention {
	out := []Attention{}
	for _, a := range v.Attention {
		if a.ObjectiveID == t.Assignment.ObjectiveID && (a.TaskID == "" || a.TaskID == t.Assignment.TaskID) {
			out = append(out, a)
		}
	}
	return out
}
