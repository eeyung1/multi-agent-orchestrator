package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func TestPostWorkerReviewFailureBlocksBothStateLayers(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed-worker-output", true: "controller-owned-commit"}[native], func(t *testing.T) {
			d, c := fixture(t)
			v := task("first")
			if native {
				d, c = nativeFixture(t)
				v.Capability = "codex"
			}
			t.Setenv("ORCHESTRATOR_UNOWNED", "1")
			p := admit(t, d, c, v)
			r, err := Run(context.Background(), d, p.ID, c)
			if err != nil {
				t.Fatal(err)
			}
			s, err := scheduler.Read(d)
			if err != nil {
				t.Fatal(err)
			}
			task := s.Tasks[p.ID+"::first"]
			if task.Status != "BLOCKED" || task.Token == "" || task.Blocker == "" || len(s.Results) != 0 || len(r.Decisions) == 0 {
				t.Fatalf("review failure not persisted: %+v; %+v", task, r)
			}
			status, err := coordination.SessionStatus(d, task.SessionID)
			if err != nil || status != "BLOCKED" {
				t.Fatalf("post-worker review failure left operational WORKING: %s %v", status, err)
			}
			if native && !strings.Contains(task.Blocker, "unowned") {
				t.Fatal("worker scope enforcement was weakened")
			}
			claims, err := coordination.Claims(d)
			if err != nil || len(claims) != 1 || claims[0].Token != task.Token || claims[0].SessionID != task.SessionID {
				t.Fatalf("blocked ownership was lost: %+v %v", claims, err)
			}
		})
	}
}
