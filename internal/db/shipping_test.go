package db

import (
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Shipping options and the reviewed comparison must survive a daemon restart,
// and only the successful Review completion may grant that generation.
func TestShippingReviewAuthorityIsDurableAndInvalidated(t *testing.T) {
	d := openTestDB(t)
	repo, err := d.InsertRepo("/project", "git@github.com:owner/project.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	run, err := d.InsertRunWithShipping(repo.ID, "feature", head, head, nil, "", "", "", "main", false, nil, &types.ShippingOptions{Mode: types.ShippingFull})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.GetRun(run.ID)
	if err != nil || got.Shipping == nil || got.Shipping.Mode != types.ShippingFull {
		t.Fatalf("pin: %+v, %v", got, err)
	}
	sr, _ := d.InsertStepResult(run.ID, types.StepReview)
	c := ReviewComparison{SourceRepo: "owner/project", SourceBranch: "feature", TargetBranch: "main", TargetSHA: head, HeadSHA: head, MergeBaseSHA: head, DiffDigest: strings.Repeat("b", 64)}
	first, err := d.CaptureReviewComparison(run.ID, c)
	if err != nil || first.Generation != 1 || first.Approved {
		t.Fatalf("candidate: %+v %v", first, err)
	}
	if err := d.CompleteReviewStep(sr.ID, run.ID, strings.Repeat("c", 40), 0, 1, "", first.Generation); err == nil {
		t.Fatal("wrong comparison granted review authority")
	}
	if err := d.CompleteReviewStep(sr.ID, run.ID, head, 0, 1, "", first.Generation); err != nil {
		t.Fatal(err)
	}
	approved, _ := d.GetReviewComparison(run.ID)
	if !approved.Approved {
		t.Fatal("successful exact Review did not grant authority")
	}
	if err := d.SetRunCIReadyWithReason(run.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := d.ResetStepsFrom(run.ID, types.StepReview.Order()); err != nil {
		t.Fatal(err)
	}
	invalid, _ := d.GetReviewComparison(run.ID)
	if invalid.Approved {
		t.Fatal("reset preserved old proof")
	}
	reset, _ := d.GetRun(run.ID)
	if reset.CIReadyAt != nil || reset.CIReadyNoCI {
		t.Fatal("reset retained readiness from old review generation")
	}
	next, err := d.CaptureReviewComparison(run.ID, c)
	if err != nil || next.Generation != 2 {
		t.Fatalf("generation rollback: %+v %v", next, err)
	}
	if err := d.CompleteReviewStep(sr.ID, run.ID, head, 0, 1, "", first.Generation); err == nil {
		t.Fatal("superseded same-head generation granted review authority")
	}
	stale, _ := d.GetReviewComparison(run.ID)
	if stale.Approved {
		t.Fatal("stale completion advanced authority")
	}
}

func TestShippingRunRejectsUnsupportedModes(t *testing.T) {
	d := openTestDB(t)
	repo, _ := d.InsertRepo("/project", "git@github.com:owner/project.git", "main")
	if _, err := d.InsertRunWithShipping(repo.ID, "feature", "head", "base", nil, "", "", "", "", false, nil, &types.ShippingOptions{Mode: "skip-ci"}); err == nil {
		t.Fatal("unsupported mode admitted")
	}
	runs, _ := d.GetRunsByRepo(repo.ID)
	if len(runs) != 0 {
		t.Fatal("invalid options created a run")
	}
}
