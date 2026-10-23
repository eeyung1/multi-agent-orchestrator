package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func WriteJSON(out io.Writer, value any) error { return json.NewEncoder(out).Encode(value) }

func WriteHuman(out io.Writer, v Snapshot) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	max := "unknown"
	if v.MaxWorkers != nil {
		max = strconv.Itoa(*v.MaxWorkers)
	}
	available := "unknown outside owning host"
	if v.System.AvailableWorkers != nil {
		available = strconv.Itoa(*v.System.AvailableWorkers)
	}
	fmt.Fprintf(w, "ORCHESTRATOR\nWorkspace: %q\nSchema: %d  Revision: %d  Initialized: %t\nActive objectives: %s\nWorkers: %d assigned / %d registry-ready / %s available; max_workers=%s (%s)\nTasks: %d complete / %d blocked / %d need human action\n", v.Workspace, v.StateSchema, v.Revision, v.Initialized, quotedList(v.ActiveObjectives), v.System.ActiveAssignments, v.System.ReadyWorkers, available, max, v.ConfigurationFrom, v.System.CompletedTasks, v.System.BlockedTasks, v.System.HumanActionTasks)
	fmt.Fprintln(w, "\nPLANNER\nRUN\tAGENT\tSESSION\tINCARNATION\tACTIVE\tLIVE\tREQUEST")
	for _, p := range v.Planners {
		fmt.Fprintf(w, "%q\t%q\t%q\t%d\t%t\t%t\t%s\n", p.RunID, p.AgentID, p.SessionID, p.Incarnation, p.Active, p.Live, p.RequestStatus)
	}
	for _, p := range v.Planners {
		fmt.Fprintf(w, "  Binding=%q request=%q reason=%q human_decision=%q\n", p.BindingID, p.RequestID, p.Reason, p.Decision)
	}
	fmt.Fprintln(w, "\nAGENTS\nPROVIDER / RUNTIME\tAGENT / SESSION\tINCARNATION\tROLE\tSTATE\tLIVENESS\tCONNECTION\tCAPABILITIES")
	for _, a := range v.Agents {
		live := "not live"
		if a.Live {
			live = "live"
		}
		if a.Stale {
			live = "STALE"
		}
		if !a.Current {
			live = "superseded"
		}
		fmt.Fprintf(w, "%q / %q\t%q / %q\t%d\t%s\t%s\t%s\t%s\t%q\n", a.Metadata.Provider, a.Metadata.Runtime, a.AgentID, a.SessionID, a.Incarnation, a.AssignedRole, a.Status, live, a.Connection, a.Capabilities)
	}
	fmt.Fprintln(w, "\nTASKS (Go-authoritative state; ASSIGNED is a reservation, not proof a process is alive)\nOBJECTIVE / TASK\tOWNER\tGENERATION\tSTATE / DISPATCH\tCLAIM\tBRANCH\tWORKTREE")
	for _, t := range v.Tasks {
		owner := "unassigned"
		if t.Owner != nil {
			owner = t.Owner.Mode + ":" + t.Owner.OperationalSession
			if t.Owner.Mode == "enrolled" {
				owner = string(t.Owner.AgentID) + "/" + string(t.Owner.SessionID)
			}
		}
		fmt.Fprintf(w, "%q / %q\t%q\t%d\t%s / %s\t%s\t%q\t%q\n", t.Assignment.ObjectiveID, t.Assignment.TaskID, owner, t.Assignment.Generation, t.Status, t.DispatchPhase, t.Claim, t.Branch, t.Worktree)
		if t.Progress != "" {
			fmt.Fprintf(w, "  Agent report (%s): %s\n", t.LastMessage, quotedSummary(t.Progress))
		}
		if t.Blocker != "" {
			fmt.Fprintf(w, "  Go blocker: %s\n", quotedSummary(t.Blocker))
		}
		if t.ValidatedHead != "" {
			fmt.Fprintf(w, "  Go validation: %s head=%q review=%s\n", t.ValidationStatus, t.ValidatedHead, t.ReviewStatus)
		}
	}
	fmt.Fprintln(w, "\nATTENTION")
	for _, a := range v.Attention {
		fmt.Fprintf(w, "[%s] %s objective=%q task=%q session=%q gen=%d: %s\n  Action: %s\n", a.Severity, a.Code, a.ObjectiveID, a.TaskID, a.SessionID, a.Generation, quotedSummary(a.Message), quotedSummary(a.Action))
	}
	if len(v.Attention) == 0 {
		fmt.Fprintln(w, "No derived attention items.")
	}
	return w.Flush()
}
func quotedSummary(s string) string {
	r := []rune(s)
	if len(r) > 512 {
		s = string(r[:512]) + "…"
	}
	return strconv.Quote(s)
}
func quotedList(s []string) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = strconv.Quote(v)
	}
	return strings.Join(parts, ", ")
}

// watch has one synchronous capture/write loop and no background goroutines.
// ticks is a trusted deterministic test seam; normal operation owns its ticker.
func watch(ctx context.Context, capture func(context.Context) (Snapshot, error), out io.Writer, jsonMode bool, interval time.Duration, updates int, ticks <-chan time.Time) error {
	if ticks == nil {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for n := 0; ; {
		if ctx.Err() != nil {
			return nil
		}
		v, err := capture(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, ErrChanging) {
				select {
				case <-ctx.Done():
					return nil
				case _, ok := <-ticks:
					if !ok {
						return nil
					}
					continue
				}
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		if jsonMode {
			err = WriteJSON(out, v)
		} else {
			if n > 0 {
				_, err = fmt.Fprintf(out, "\n--- snapshot revision=%d at=%s ---\n", v.Revision, v.ObservedAt.Format(time.RFC3339Nano))
			}
			if err == nil {
				err = WriteHuman(out, v)
			}
		}
		if err != nil {
			return err
		}
		if updates > 0 && n+1 >= updates {
			return nil
		}
		n++
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
		}
	}
}
