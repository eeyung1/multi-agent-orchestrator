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
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/providers"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func authorizeBlockedRecovery(reader *bufio.Reader, out io.Writer, primary, objectiveID string) error {
	snap, err := scheduler.Read(primary)
	if err != nil {
		return err
	}
	var keys []string
	for key, task := range snap.Tasks {
		if task.ObjectiveID == objectiveID && task.Status == "BLOCKED" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		task := snap.Tasks[key]
		fmt.Fprintf(out, "Blocked task %s (attempt %d): %s\n", task.ID, task.Attempt, task.Blocker)
		if task.Branch != "" {
			fmt.Fprintf(out, "Preserved branch: %s\n", task.Branch)
		}
		yes, err := confirm(reader, out, fmt.Sprintf("Authorize recovery of blocked task %s with a fresh assignment? [y/N] ", task.ID))
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !yes {
			continue
		}
		if err := scheduler.AuthorizeRecovery(primary, objectiveID, task.ID, task.AssignmentGeneration, "interactive user explicitly authorized blocked-task recovery"); err != nil {
			return fmt.Errorf("authorize recovery for %s: %w", task.ID, err)
		}
		fmt.Fprintf(out, "Recovery authorized for %s; prior assignment remains preserved as audit history.\n", task.ID)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "planner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) (result error) {
	flags := flag.NewFlagSet("planner", flag.ContinueOnError)
	flags.SetOutput(out)
	workspace := flags.String("workspace", "", "target Git repository (asked if omitted)")
	configRoot, _ := os.UserConfigDir()
	configPath := flags.String("config", filepath.Join(configRoot, "orchestrator", "planner.json"), "trusted provider/worker/validator configuration")
	requested := flags.String("objective", "", "natural-language objective (asked if omitted)")
	resume := flags.String("resume", "", "resume a saved objective; otherwise choose interactively")
	var plannerID string
	flags.StringVar(&plannerID, "planner", "", "built-in Planner ID (codex, claude, gemini)")
	flags.StringVar(&plannerID, "provider", "", "alias for --planner")
	workerIDs := flags.String("workers", "", "comma-separated built-in worker providers")
	maxWorkers := flags.Int("max-workers", 0, "maximum concurrent workers (defaults to trusted policy)")
	list := flags.Bool("list-providers", false, "list installed compatible adapters without running them")
	enrolledWorkerSocket := flags.String("enrolled-worker-socket", "", "host private local enrolled worker attachment; trusted enrolled_capabilities policy required")
	enrolledSocket := flags.String("enrolled-planner-socket", "", "host private local attachment and explicitly select an enrolled Planner instead of a managed provider")
	registryStale := flags.Duration("registry-stale-after", 90*time.Second, "registry liveness policy; must match on reopen")
	planningTimeout := flags.Duration("planning-timeout", 5*time.Minute, "maximum enrolled Planner response duration")
	configurePreparation := flags.Bool("configure-preparation", false, "explicitly update trusted worktree preparation policy")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *enrolledSocket != "" && (plannerID != "" || *planningTimeout <= 0) {
		return errors.New("choose either a managed Planner or enrolled Planner mode with positive timeout")
	}
	if *list {
		listProviders(out, *workspace)
		return nil
	}
	configExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})
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
	if plannerID != "" {
		if _, err := providers.Resolve(plannerID, primary); err != nil {
			return err
		}
	}
	if *workerIDs != "" {
		for _, id := range strings.Split(*workerIDs, ",") {
			if _, err := providers.Resolve(strings.TrimSpace(id), primary); err != nil {
				return err
			}
		}
	}
	if err := customConfigTrust(reader, out, *configPath, primary, configExplicit); err != nil {
		return err
	}
	c, err := controller.ReadConfig(*configPath)
	missingPolicy := os.IsNotExist(err)
	if err != nil && !missingPolicy {
		return fmt.Errorf("load trusted Planner configuration: %w", err)
	}
	if *enrolledSocket == "" && *resume == "" && c.Planner.Program == "" && c.Planner.Endpoint == "" && len(available(primary, true)) == 0 {
		return fmt.Errorf("no compatible Planner CLI is installed on trusted PATH; install/authenticate Codex, Claude Code or Gemini CLI, or use --config for a custom provider")
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
	if *configurePreparation {
		if missingPolicy {
			return errors.New("no existing trusted policy to update; complete initial policy bootstrap first")
		}
		if err := configurePreparationPolicy(reader, out, primary, *configPath, &c); err != nil {
			return err
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
	var saved state.Objective
	if objectiveID != "" {
		_, saved, err = scheduler.Restore(primary, objectiveID)
		if err != nil {
			return err
		}
		if saved.PlannerEvidence != nil && plannerID != "" {
			return errors.New("saved objective used an enrolled Planner; resume does not select a replacement Planner")
		}
		if saved.PlannerEvidence == nil && *enrolledSocket != "" {
			return errors.New("saved objective used a managed Planner; choose enrolled mode for a new objective")
		}
	}
	var enrolled *planner.Enrolled
	var proposer planner.Proposer
	if *enrolledSocket != "" || saved.PlannerEvidence != nil {
		c.Planner = planner.Provider{}
		if len(saved.WorkerProviders) > 0 {
			if *workerIDs != "" && !samePool(strings.Split(*workerIDs, ","), saved.WorkerProviders) {
				return errors.New("saved objective worker pool differs")
			}
			*workerIDs = strings.Join(saved.WorkerProviders, ",")
		}
		if err := activateWorkers(reader, out, primary, &c, *workerIDs); err != nil {
			return err
		}
		if objectiveID == "" {
			selected, cleanup, err := selectEnrolled(ctx, reader, out, primary, *enrolledSocket, *registryStale, *planningTimeout)
			if err != nil {
				return err
			}
			defer func() { result = errors.Join(result, cleanup()) }()
			enrolled = selected
			proposer = enrolled
		}
	} else {
		if err := activate(reader, out, primary, &c, plannerID, *workerIDs, saved); err != nil {
			return err
		}
		proposer = c.Planner
	}
	if *maxWorkers < 0 {
		return errors.New("max-workers must be positive")
	}
	if *maxWorkers > 0 {
		c.MaxWorkers = *maxWorkers
	}
	if missingPolicy {
		if err := bootstrap(reader, out, primary, *configPath, &c); err != nil {
			return err
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.ValidateAdapters(primary); err != nil {
		return err
	}
	if objectiveID != "" && saved.PolicyDigest != controller.PolicyDigest(c) {
		fmt.Fprintln(out, "The trusted execution policy differs from the policy previously authorized for this saved objective.")
		yes, err := confirm(reader, out, "Authorize this saved objective to continue under the currently displayed/configured trusted execution policy? [y/N] ")
		if err != nil {
			return err
		}
		if !yes {
			return errors.New("saved objective policy reauthorization was not authorized")
		}
		if err := controller.ReauthorizePolicy(primary, objectiveID, c, "interactive user explicitly reauthorized saved objective execution policy"); err != nil {
			return err
		}
		fmt.Fprintln(out, "Saved objective execution policy reauthorized. Plan, task ownership, results, and repository instruction boundary were unchanged.")
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
			proposal, err = proposer.Propose(ctx, planner.Request{Objective: text, Recon: r, Capabilities: c.Capabilities(), Validators: c.Checks(), AllowedScopes: c.AllowedScopes, RequiredValidators: c.RequiredValidators})
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
		human := "interactive user confirmed displayed objective plan and configured execution policy"
		if enrolled != nil {
			err = controller.AdmitEnrolled(primary, proposal.Plan, c, r, human, enrolled.Evidence())
		} else {
			err = controller.Admit(primary, proposal.Plan, c, r, human)
		}
		if err != nil {
			return err
		}
		objectiveID = proposal.Plan.ID
	}
	if objectiveID != "" {
		if err := authorizeBlockedRecovery(reader, out, primary, objectiveID); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "ORCHESTRATOR is scheduling eligible work and collecting evidence. Existing assignments will not be relaunched.")
	if len(c.EnrolledCapabilities) > 0 {
		if enrolled != nil {
			c.Runtime = &controller.EnrolledWorkers{Registry: enrolled.Registry, Server: enrolled.Server}
		}
		cleanup, err := prepareEnrolledWorkers(ctx, reader, out, primary, *enrolledWorkerSocket, *registryStale, &c)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, cleanup()) }()
	} else if *enrolledWorkerSocket != "" {
		return errors.New("configure explicit enrolled_capabilities before worker attachment")
	}
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
