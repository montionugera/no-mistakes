package shipping

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/testgit"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type receiptForge struct {
	scm.Host
	facts   scm.PRFacts
	checks  []scm.Check
	observe func()
}

func (h *receiptForge) GetPRFacts(context.Context, *scm.PR) (scm.PRFacts, error) {
	if h.observe != nil {
		h.observe()
	}
	return h.facts, nil
}
func (h *receiptForge) GetChecks(context.Context, *scm.PR) ([]scm.Check, error) { return h.checks, nil }

type exportFixture struct {
	database *db.DB
	repo     *db.Repo
	run      *db.Run
	forge    *receiptForge
	dir      string
}

func receiptGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	binary, err := testgit.RealGit()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func newExportFixture(t *testing.T) *exportFixture {
	return newExportFixtureWithDraft(t, nil)
}

func newExportFixtureWithDraft(t *testing.T, draft *bool) *exportFixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "worktree")
	remote := filepath.Join(root, "origin.git")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	receiptGit(t, root, "init", "--bare", remote)
	receiptGit(t, dir, "init", "-b", "main")
	receiptGit(t, dir, "config", "user.email", "fixture@example.test")
	receiptGit(t, dir, "config", "user.name", "Fixture")
	receiptGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	receiptGit(t, dir, "add", "source.txt")
	receiptGit(t, dir, "commit", "-m", "base")
	base := receiptGit(t, dir, "rev-parse", "HEAD")
	receiptGit(t, dir, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	receiptGit(t, dir, "commit", "-am", "feature")
	head := receiptGit(t, dir, "rev-parse", "HEAD")
	receiptGit(t, dir, "push", remote, "main", "feature")
	url := "https://github.com/owner/project.git"
	receiptGit(t, dir, "config", "url."+remote+".insteadOf", url)
	database, err := db.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	repo, err := database.InsertRepo(dir, url, "main")
	if err != nil {
		t.Fatal(err)
	}
	intent := &db.RunIntent{Summary: "exact user intent\n", Source: db.RunIntentSourceAgent, Score: 1}
	run, err := database.InsertRunWithShipping(repo.ID, "feature", head, base, intent, "", "", "", "main", false, nil, &types.ShippingOptions{Mode: types.ShippingFull, Draft: draft})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetRunWorktreeDir(run.ID, dir); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/owner/project/pull/7"
	if err := database.UpdateRunPRURL(run.ID, prURL); err != nil {
		t.Fatal(err)
	}
	_, digest, err := Compare(context.Background(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.CaptureReviewComparison(run.ID, db.ReviewComparison{SourceRepo: "owner/project", SourceBranch: "feature", TargetBranch: "main", TargetSHA: base, HeadSHA: head, MergeBaseSHA: base, DiffDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []types.StepName{types.StepIntent, types.StepRebase, types.StepReview, types.StepTest, types.StepDocument, types.StepLint, types.StepPush, types.StepPR, types.StepCI} {
		sr, err := database.InsertStepResult(run.ID, step)
		if err != nil {
			t.Fatal(err)
		}
		if step == types.StepReview {
			err = database.CompleteReviewStep(sr.ID, run.ID, head, 0, 1, "", 1)
		} else if step == types.StepCI {
			err = database.UpdateStepStatus(sr.ID, types.StepStatusRunning)
		} else {
			err = database.CompleteStep(sr.ID, 0, 1, "")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpdateRunPushBinding(run.ID, db.PushBinding{HeadSHA: head, TargetKind: "upstream", TargetFingerprint: Digest([]byte(url)), Ref: "refs/heads/feature"}); err != nil {
		t.Fatal(err)
	}
	forge := &receiptForge{facts: scm.PRFacts{URL: prURL, State: scm.PRStateOpen, SourceRepo: "owner/project", SourceBranch: "feature", HeadSHA: head, TargetRepo: "owner/project", TargetBranch: "main", TargetSHA: base}, checks: []scm.Check{{Name: "approval", Bucket: scm.CheckBucketPending}}}
	return &exportFixture{database, repo, run, forge, dir}
}

func TestExportRefusesPinnedDraftDrift(t *testing.T) {
	draft := false
	f := newExportFixtureWithDraft(t, &draft)
	f.forge.facts.Draft = true
	if _, err := Export(context.Background(), f.database, f.repo, f.run.ID, "review", f.forge); err == nil {
		t.Fatal("draft drift accepted")
	}
}

func TestExportReviewBeforeApprovalThenExactCIReady(t *testing.T) {
	f := newExportFixture(t)
	ctx := context.Background()
	raw, err := Export(ctx, f.database, f.repo, f.run.ID, "review", f.forge)
	if err != nil {
		t.Fatal(err)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Phase != "review" || p.Generation != 1 || p.Steps[len(p.Steps)-1].Status != "running" || p.IntentDigest != Digest([]byte("exact user intent\n")) {
		t.Fatalf("wrong review proof: %+v", p)
	}
	if p.Review.Findings == nil {
		t.Fatal("empty findings emitted null")
	}
	if _, err := Export(ctx, f.database, f.repo, f.run.ID, "ci-ready", f.forge); err == nil {
		t.Fatal("pending CI claimed ready")
	}
	if err := f.database.SetRunCIReadyWithReason(f.run.ID, true, false); err != nil {
		t.Fatal(err)
	}
	f.forge.checks = []scm.Check{{Name: "approval", Bucket: scm.CheckBucketPass}, {Name: "unaffected optional lane", Bucket: scm.CheckBucketSkip}}
	if _, err := Export(ctx, f.database, f.repo, f.run.ID, "ci-ready", f.forge); err != nil {
		t.Fatal(err)
	}
	f.forge.checks = nil
	if _, err := Export(ctx, f.database, f.repo, f.run.ID, "ci-ready", f.forge); err == nil {
		t.Fatal("empty checks accepted")
	}
}

func TestExportRefusesRacesOverridesSkipsAndActionableFindings(t *testing.T) {
	for _, kind := range []string{"generation", "head", "target", "override", "skip", "actionable", "publication", "dirty"} {
		t.Run(kind, func(t *testing.T) {
			f := newExportFixture(t)
			rows, _ := f.database.GetStepsByRun(f.run.ID)
			switch kind {
			case "generation":
				f.forge.observe = func() {
					c, _ := f.database.GetReviewComparison(f.run.ID)
					if _, err := f.database.CaptureReviewComparison(f.run.ID, *c); err != nil {
						t.Fatal(err)
					}
				}
			case "head":
				f.forge.facts.HeadSHA = strings.Repeat("a", 40)
			case "target":
				f.forge.facts.TargetSHA = strings.Repeat("b", 40)
			case "override":
				if err := f.database.SetStepOverrideReason(rows[3].ID, "not authorized"); err != nil {
					t.Fatal(err)
				}
			case "skip":
				if err := f.database.UpdateStepStatus(rows[6].ID, types.StepStatusSkipped); err != nil {
					t.Fatal(err)
				}
			case "actionable":
				if err := f.database.SetStepFindings(rows[2].ID, `{"summary":"remaining","items":[{"id":"f1","severity":"error","description":"bug","action":"ask-user"}]}`); err != nil {
					t.Fatal(err)
				}
			case "publication":
				if err := f.database.UpdateRunPushBinding(f.run.ID, db.PushBinding{HeadSHA: strings.Repeat("a", 40), Ref: "refs/heads/feature"}); err != nil {
					t.Fatal(err)
				}
			case "dirty":
				if err := os.WriteFile(filepath.Join(f.dir, "dirty"), []byte("unreviewed"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Export(context.Background(), f.database, f.repo, f.run.ID, "review", f.forge); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}

func TestExportFullRefusesUnintegratedCurrentTarget(t *testing.T) {
	f := newExportFixture(t)
	receiptGit(t, f.dir, "switch", "main")
	if err := os.WriteFile(filepath.Join(f.dir, "target-only"), []byte("advance"), 0644); err != nil {
		t.Fatal(err)
	}
	receiptGit(t, f.dir, "add", "target-only")
	receiptGit(t, f.dir, "commit", "-m", "advance target")
	target := receiptGit(t, f.dir, "rev-parse", "HEAD")
	receiptGit(t, f.dir, "push", f.repo.UpstreamURL, "main")
	receiptGit(t, f.dir, "switch", "feature")
	base, digest, err := Compare(context.Background(), f.dir, target, f.run.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.database.CaptureReviewComparison(f.run.ID, db.ReviewComparison{SourceRepo: "owner/project", SourceBranch: "feature", TargetBranch: "main", TargetSHA: target, HeadSHA: f.run.HeadSHA, MergeBaseSHA: base, DiffDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.database.GetStepsByRun(f.run.ID)
	if err := f.database.CompleteReviewStep(rows[2].ID, f.run.ID, f.run.HeadSHA, 0, 1, "", c.Generation); err != nil {
		t.Fatal(err)
	}
	f.forge.facts.TargetSHA = target
	if _, err := Export(context.Background(), f.database, f.repo, f.run.ID, "review", f.forge); err == nil {
		t.Fatal("full proof admitted target absent from reviewed head")
	}
}
