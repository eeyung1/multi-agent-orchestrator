package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "planner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("planner", flag.ContinueOnError)
	flags.SetOutput(out)
	workspace := flags.String("workspace", "", "target Git repository (asked if omitted)")
	configRoot, _ := os.UserConfigDir()
	configPath := flags.String("config", filepath.Join(configRoot, "orchestrator", "planner.json"), "trusted provider/worker/validator configuration")
	requested := flags.String("objective", "", "natural-language objective (asked if omitted)")
	resume := flags.String("resume", "", "resume a saved objective; otherwise choose interactively")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	reader := bufio.NewReader(in)
	if *workspace == "" {
		value, err := ask(reader, out, "Target repository path: ")
		if err != nil {
			return err
		}
		*workspace = value
	}
	primary, err := repository.Primary(*workspace)
	if err != nil {
		return err
	}
	c, err := controller.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load trusted Planner configuration: %w", err)
	}
	fmt.Fprintf(out, "Repository detected: %s\n", primary)
	preflight, err := enrollment.Preflight(primary)
	if err != nil {
		return err
	}
	if err := enrollment.RequireEnrolled(primary); err != nil {
		if preflight.Disposition != enrollment.Native {
			return fmt.Errorf("decision required: reconcile legacy authority before execution (%v)", preflight.Findings)
		}
		yes, err := confirm(reader, out, "Authorize ORCHESTRATOR as the sole coordination authority for this repository? [y/N] ")
		if err != nil {
			return err
		}
		if !yes {
			return errors.New("repository enrollment was not authorized")
		}
		if _, err := enrollment.Enroll(primary, enrollment.Native, "interactive user explicitly authorized Planner enrollment"); err != nil {
			return err
		}
	} else if preflight.Disposition != enrollment.Native {
		s, err := scheduler.Read(primary)
		if err != nil {
			return err
		}
		if s.Authority.Enrollment == string(enrollment.Native) {
			return errors.New("decision required: competing instructions/tooling appeared after native enrollment")
		}
	}
	objectiveID := *resume
	if objectiveID == "" && *requested == "" {
		s, err := scheduler.Read(primary)
		if err != nil {
			return err
		}
		var ids []string
		for id, o := range s.Objectives {
			if o.AuthorizedBy != "" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			for i, id := range ids {
				o := s.Objectives[id]
				fmt.Fprintf(out, "%d. %s [%s]\n", i+1, o.Description, o.Status)
			}
			choice, err := ask(reader, out, "Resume an objective number, or press Enter to describe new work: ")
			if err != nil {
				return err
			}
			if choice != "" {
				var n int
				if _, err := fmt.Sscanf(choice, "%d", &n); err != nil || n < 1 || n > len(ids) {
					return errors.New("invalid objective selection")
				}
				objectiveID = ids[n-1]
			}
		}
	}
	if objectiveID == "" {
		text := strings.TrimSpace(*requested)
		if text == "" {
			text, err = ask(reader, out, "What would you like the agents to accomplish? ")
			if err != nil {
				return err
			}
		}
		if text == "" {
			return errors.New("an objective is required")
		}
		r, err := recon.Inspect(primary, text)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Recon: %d files, %d instruction documents, %d existing tasks.\n", len(r.Files), len(r.Instructions), len(r.Existing))
		yes, err := confirm(reader, out, "Send this bounded repository context to the configured planning provider (provider costs/privacy policy apply)? [y/N] ")
		if err != nil {
			return err
		}
		if !yes {
			return errors.New("planning provider invocation was not authorized")
		}
		var proposal planner.Proposal
		for turn := 0; turn < 4; turn++ {
			proposal, err = c.Planner.Propose(ctx, planner.Request{Objective: text, Recon: r, Capabilities: c.Capabilities(), Validators: c.Checks(), AllowedScopes: c.AllowedScopes, RequiredValidators: c.RequiredValidators})
			if err != nil {
				return err
			}
			if len(proposal.Questions) == 0 {
				break
			}
			if turn == 3 {
				return errors.New("decision required: planning clarification limit reached")
			}
			for _, question := range proposal.Questions {
				answer, err := ask(reader, out, question+"\nYour decision: ")
				if err != nil {
					return err
				}
				if answer == "" {
					return errors.New("decision required: clarification was not answered")
				}
				text += "\nUser clarification to " + question + ": " + answer
			}
		}
		if proposal.Plan.Objective != text {
			return errors.New("proposal changed the user objective")
		}
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		proposal.Plan.ID = "objective-" + hex.EncodeToString(random[:])
		// Validate before asking for approval, so the user reviews an admissible
		// concrete plan rather than granting permission to arbitrary model output.
		if err := validateProposal(primary, proposal, c); err != nil {
			return fmt.Errorf("decision required: %w", err)
		}
		for _, t := range proposal.Plan.Tasks {
			fmt.Fprintf(out, "- %s: %s\n  scope=%s dependencies=%v checks=%v worker=%s\n  acceptance=%v\n", t.ID, t.Description, t.Scope, t.DependsOn, t.Validation, t.Capability, t.AcceptanceCriteria)
		}
		fmt.Fprintf(out, "Up to %d configured workers can run concurrently. Workers commit local feature branches; nothing merges or deploys.\n", c.MaxWorkers)
		yes, err = confirm(reader, out, "Authorize this plan and its configured worker/validation commands? [y/N] ")
		if err != nil {
			return err
		}
		if !yes {
			return errors.New("plan execution was not authorized")
		}
		if err := controller.Admit(primary, proposal.Plan, c, r, "interactive user confirmed displayed objective plan and configured execution policy"); err != nil {
			return err
		}
		objectiveID = proposal.Plan.ID
	}
	fmt.Fprintln(out, "ORCHESTRATOR is scheduling eligible work and collecting evidence. Existing assignments will not be relaunched.")
	report, err := controller.Run(ctx, primary, objectiveID, c)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	if report.ReviewStatus == "AWAITING_REVIEW" {
		choice, err := ask(reader, out, "Human review decision [approve/changes/leave]: ")
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		decision := ""
		switch strings.ToLower(choice) {
		case "approve":
			decision = "APPROVED"
		case "changes":
			decision = "CHANGES_REQUESTED"
		case "", "leave":
			return nil
		default:
			return errors.New("unrecognized review decision; review remains pending")
		}
		if err := review.Decide(primary, objectiveID, decision, "interactive human reviewer"); err != nil {
			return err
		}
		fmt.Fprintf(out, "Review recorded: %s. No merge or deployment performed.\n", decision)
	}
	return nil
}

func ask(r *bufio.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
func confirm(r *bufio.Reader, out io.Writer, prompt string) (bool, error) {
	answer, err := ask(r, out, prompt)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(answer, "yes") || strings.EqualFold(answer, "y"), nil
}
