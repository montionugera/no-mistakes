package pipeline

import (
	"context"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"strings"
	"testing"
	"time"
)

// A clean findings list cannot waive a Review coverage gate in strict mode.
func TestShippingReviewGateCannotBeApprovedWithoutFullReview(t *testing.T) {
	database, p, run, repo := setupTest(t)
	run.Shipping = &types.ShippingOptions{Mode: types.ShippingFull}
	step := &mockStep{name: types.StepReview, outcome: &StepOutcome{NeedsApproval: true, Findings: `{"items":[]}`, ReviewApprovedHeadSHA: run.HeadSHA, ReviewComparisonGeneration: 1}}
	e := NewExecutor(database, p, &config.Config{}, nil, []Step{step}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- e.Execute(ctx, run, repo, dir) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.mu.Lock()
		waiting := e.waiting
		e.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("review did not park")
		}
		time.Sleep(time.Millisecond)
	}
	if err := e.Respond(types.StepReview, types.ActionApprove, nil); err == nil || !strings.Contains(err.Error(), "strict Review") {
		t.Fatalf("coverage gate approval = %v", err)
	}
	if err := e.Respond(types.StepReview, types.ActionSkip, nil); err == nil {
		t.Fatal("strict skip accepted")
	}
	if err := e.Respond(types.StepReview, types.ActionAbort, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("aborted Review succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not settle")
	}
	got, err := database.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewApprovedHeadSHA != nil || got.Status != types.RunFailed {
		t.Fatalf("unreviewed run gained authority: %+v", got)
	}
}

func TestShippingRevalidationRevokesDurableAndPublicReadiness(t *testing.T) {
	database, p, old, repo := setupTest(t)
	run, err := database.InsertRunWithShipping(repo.ID, "feature", old.HeadSHA, old.BaseSHA, nil, "", "", "", "main", false, nil, &types.ShippingOptions{Mode: types.ShippingFull})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetRunCIReadyWithReason(run.ID, true, false); err != nil {
		t.Fatal(err)
	}
	run, err = database.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(database, p, &config.Config{}, nil, []Step{newPassStep(types.StepRebase), newPassStep(types.StepReview), newPassStep(types.StepCI)}, nil)
	events := collectEvents(e)
	if _, err := e.prepareRestart(run, repo, types.StepRebase, 2); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CIReadyAt != nil || run.CIReadyAt != nil {
		t.Fatal("restart retained old comparison readiness")
	}
	var revoked bool
	for _, event := range events.events {
		if event.Type == ipc.EventCIReadinessChanged && event.CIReady != nil && !*event.CIReady {
			revoked = true
		}
	}
	if !revoked {
		t.Fatal("restart omitted public readiness revocation")
	}
}
