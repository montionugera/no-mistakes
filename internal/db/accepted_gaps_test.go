package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

const gapVerdictFinding = `{"id":"test-1","severity":"warning","action":"ask-user","category":"test-verdict","description":"live validation verdict: inconclusive (1 of 2 scenarios were driven live against the product); untested: broker timeout"}`

func gapFindings(verdict string, extra ...string) string {
	items := append([]string{gapVerdictFinding}, extra...)
	return `{"findings":[` + strings.Join(items, ",") + `],"verdict":"` + verdict + `"}`
}

func gapRow(findings string) *StepResult {
	return &StepResult{StepName: types.StepTest, Status: types.StepStatusAwaitingApproval, FindingsJSON: &findings}
}

func gapRequest(ids ...string) AcceptedGaps {
	return AcceptedGaps{FindingIDs: ids, Reason: "owner approved via first mate, 2026-10-14", CoveredBy: "TestBrokerTimeout, TestRetry"}
}

func TestAcceptedGapRefusal(t *testing.T) {
	exit1 := 1
	other := `{"id":"test-2","severity":"error","action":"ask-user","description":"unrelated defect"}`
	noop := `{"id":"test-3","severity":"info","action":"no-op","description":"note"}`
	command := `{"id":"test-4","severity":"error","action":"auto-fix","category":"test-command","description":"command failed"}`
	timeout := `{"id":"test-agent-timeout","severity":"warning","action":"ask-user","description":"cut"}`
	unvalidated := `{"id":"test-agent-unvalidated-work","severity":"warning","action":"ask-user","description":"unvalidated"}`
	tests := []struct {
		name    string
		row     *StepResult
		strict  bool
		gaps    AcceptedGaps
		refusal string
	}{
		{"accepted", gapRow(gapFindings("inconclusive")), true, gapRequest("test-1"), ""},
		{"accepted with no-op sibling", gapRow(gapFindings("inconclusive", noop)), true, gapRequest("test-1"), ""},
		{"accepted with padded and duplicate ids", gapRow(gapFindings("inconclusive")), true, gapRequest(" test-1 ", "test-1"), ""},
		{"non-strict", gapRow(gapFindings("inconclusive")), false, gapRequest("test-1"), "use --reason"},
		{"wrong step", &StepResult{StepName: types.StepReview, FindingsJSON: ptr(gapFindings("inconclusive"))}, true, gapRequest("test-1"), "only to the Test step"},
		{"no ids", gapRow(gapFindings("inconclusive")), true, gapRequest(), "at least one finding id"},
		{"unknown id", gapRow(gapFindings("inconclusive")), true, gapRequest("nope"), `unknown finding "nope"`},
		{"missing id", gapRow(gapFindings("inconclusive", other)), true, gapRequest("test-1"), "missing: test-2"},
		{"named non-verdict finding", gapRow(gapFindings("inconclusive", other)), true, gapRequest("test-1", "test-2"), "not an acceptable gap"},
		{"no-go", gapRow(gapFindings("no-go")), true, gapRequest("test-1"), "no-go"},
		{"go verdict", gapRow(gapFindings("go")), true, gapRequest("test-1"), "only an inconclusive"},
		{"test command", gapRow(gapFindings("inconclusive", command)), true, gapRequest("test-1", "test-4"), "failing Test command"},
		{"timeout", gapRow(gapFindings("inconclusive", timeout)), true, gapRequest("test-1", "test-agent-timeout"), "timeout"},
		{"unvalidated work", gapRow(gapFindings("inconclusive", unvalidated)), true, gapRequest("test-1", "test-agent-unvalidated-work"), "unvalidated work"},
		{"exit code", &StepResult{StepName: types.StepTest, ExitCode: &exit1, FindingsJSON: ptr(gapFindings("inconclusive"))}, true, gapRequest("test-1"), "command overrides"},
		{"override reason", &StepResult{StepName: types.StepTest, OverrideReason: ptr("x"), FindingsJSON: ptr(gapFindings("inconclusive"))}, true, gapRequest("test-1"), "overrides or skips"},
		{"skip reason", &StepResult{StepName: types.StepTest, SkipReason: ptr("x"), FindingsJSON: ptr(gapFindings("inconclusive"))}, true, gapRequest("test-1"), "overrides or skips"},
		{"approval reason", &StepResult{StepName: types.StepTest, ApprovalReason: ptr("x"), FindingsJSON: ptr(gapFindings("inconclusive"))}, true, gapRequest("test-1"), "Test exceptions"},
		{"unreadable findings", gapRow("{"), true, gapRequest("test-1"), "could not be read"},
		{"no findings", &StepResult{StepName: types.StepTest}, true, gapRequest("test-1"), "no findings"},
		{"missing reason", gapRow(gapFindings("inconclusive")), true, AcceptedGaps{FindingIDs: []string{"test-1"}, CoveredBy: "TestX"}, "--reason"},
		{"short reason", gapRow(gapFindings("inconclusive")), true, AcceptedGaps{FindingIDs: []string{"test-1"}, Reason: "ok", CoveredBy: "TestX"}, "at least 10 characters"},
		{"missing covered-by", gapRow(gapFindings("inconclusive")), true, AcceptedGaps{FindingIDs: []string{"test-1"}, Reason: "owner approved via first mate"}, "--covered-by"},
		{"multiline reason", gapRow(gapFindings("inconclusive")), true, AcceptedGaps{FindingIDs: []string{"test-1"}, Reason: "owner approved\nvia first mate", CoveredBy: "TestX"}, "single line"},
		{"verdict mismatch", gapRow(gapFindings("inconclusive")), true, AcceptedGaps{FindingIDs: []string{"test-1"}, Reason: "owner approved via first mate", CoveredBy: "TestX", Verdict: "go"}, "does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.row.AcceptedGapRefusal(tt.strict, tt.gaps)
			if tt.refusal == "" {
				if got != "" {
					t.Fatalf("refused: %s", got)
				}
				return
			}
			if !strings.Contains(got, tt.refusal) {
				t.Fatalf("refusal = %q, want substring %q", got, tt.refusal)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestAcceptedGapLifecycle(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo, err := d.InsertRepo("/synthetic", "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.InsertRun(repo.ID, "feature", "head", "base")
	if err != nil {
		t.Fatal(err)
	}
	step, err := d.InsertStepResult(run.ID, types.StepTest)
	if err != nil {
		t.Fatal(err)
	}
	findings := gapFindings("inconclusive")
	if err := d.ParkStepForApproval(run.ID, step.ID, types.StepStatusAwaitingApproval, 0, 10, &findings); err != nil {
		t.Fatal(err)
	}
	parked, err := d.GetStepResult(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.StrictApprovalRefusal("") == "" {
		t.Fatal("plain strict approval of an inconclusive verdict must stay refused")
	}
	if got := parked.StrictApprovalRefusalWithGaps("", &AcceptedGaps{FindingIDs: []string{"test-1"}, Reason: "owner approved via first mate, 2026-10-14", CoveredBy: "TestX"}); got != "" {
		t.Fatalf("valid request refused: %s", got)
	}
	if _, err := d.RecordAcceptedGaps(step.ID, gapRequest("nope"), true); err == nil {
		t.Fatal("unknown id recorded")
	}
	if _, err := d.RecordAcceptedGaps(step.ID, gapRequest("test-1"), false); err == nil {
		t.Fatal("non-strict run recorded a gap")
	}
	record, err := d.RecordAcceptedGaps(step.ID, gapRequest("test-1"), true)
	if err != nil {
		t.Fatal(err)
	}
	if record.Verdict != types.TestVerdictInconclusive || record.AcceptedAt == "" || len(record.Findings) != 1 || !strings.Contains(record.Findings[0].Description, "broker timeout") || record.Findings[0].Category != types.FindingCategoryTestVerdict {
		t.Fatalf("record = %+v", record)
	}
	if err := d.CompleteStep(step.ID, 0, 10, "test.log"); err != nil {
		t.Fatal(err)
	}
	done, err := d.GetStepResult(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != types.StepStatusCompleted || done.ApprovalReason != nil || done.OverrideReason != nil || done.AcceptedGapsJSON == nil {
		t.Fatalf("completed row = %+v", done)
	}
	if got := done.StrictApprovalRefusal(""); got != "" {
		t.Fatalf("completed gap row refused: %s", got)
	}
	if got := done.TestOverrideReason(); !strings.Contains(got, "Test gap accepted (test-1)") || !strings.Contains(got, "owner approved via first mate") || !strings.Contains(got, "TestBrokerTimeout, TestRetry") {
		t.Fatalf("TestOverrideReason = %q", got)
	}
	// A record that no longer matches the row's findings is not honored.
	tampered := gapFindings("inconclusive", `{"id":"test-9","severity":"error","action":"ask-user","description":"new defect"}`)
	done.FindingsJSON = &tampered
	if done.ValidAcceptedGaps() != nil || done.StrictApprovalRefusal("") == "" {
		t.Fatal("stale record honored")
	}
	// Revalidation drops the record.
	if err := d.ResetStepsFrom(run.ID, types.StepTest.Order()); err != nil {
		t.Fatal(err)
	}
	reset, err := d.GetStepResult(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.AcceptedGapsJSON != nil {
		t.Fatalf("reset kept accepted gaps: %v", *reset.AcceptedGapsJSON)
	}
}

func TestAcceptedGapsColumnMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := d.InsertRepo("/synthetic", "origin", "main")
	run, _ := d.InsertRun(repo.ID, "feature", "head", "base")
	step, _ := d.InsertStepResult(run.ID, types.StepTest)
	if _, err := d.sql.Exec("ALTER TABLE step_results DROP COLUMN accepted_gaps_json"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	legacy, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := legacy.GetStepResult(step.ID)
	legacy.Close()
	if err != nil || got.AcceptedGapsJSON != nil {
		t.Fatalf("legacy read = %+v, %v", got, err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.hasColumn("step_results", "accepted_gaps_json") {
		t.Fatal("migration did not re-add accepted_gaps_json")
	}
}
