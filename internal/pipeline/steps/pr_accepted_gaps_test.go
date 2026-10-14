package steps

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const (
	acceptedGapTestReason    = "owner approved via first mate, 2026-10-14"
	acceptedGapTestCoveredBy = "TestBrokerTimeoutMocked; internal/broker/timeout_test.go"
)

func acceptedGapStepsForTest(t *testing.T) []*db.StepResult {
	t.Helper()
	findings := `{"findings":[{"id":"test-1","severity":"warning","action":"ask-user","category":"test-verdict","description":"live validation verdict: inconclusive (1 of 2 scenarios were driven live against the product); untested: broker timeout"}],"verdict":"inconclusive"}`
	gaps := `{"finding_ids":["test-1"],"findings":[{"id":"test-1","category":"test-verdict","description":"live validation verdict: inconclusive (1 of 2 scenarios were driven live against the product); untested: broker timeout"}],"reason":"` + acceptedGapTestReason + `","covered_by":"` + acceptedGapTestCoveredBy + `","verdict":"inconclusive","accepted_at":"2026-10-14T00:00:00Z"}`
	return []*db.StepResult{
		{ID: "review", StepName: types.StepReview, Status: types.StepStatusCompleted},
		{ID: "test", StepName: types.StepTest, Status: types.StepStatusCompleted, FindingsJSON: &findings, AcceptedGapsJSON: &gaps},
		{ID: "document", StepName: types.StepDocument, Status: types.StepStatusCompleted},
	}
}

func TestPipelineSummaryCarriesAcceptedGaps(t *testing.T) {
	t.Parallel()
	steps := acceptedGapStepsForTest(t)
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	for _, want := range []string{acceptedGapsHeading, "`test-1`", "untested: broker timeout", "Approved: " + acceptedGapTestReason, "Covered by: " + acceptedGapTestCoveredBy} {
		if !strings.Contains(md, want) {
			t.Fatalf("pipeline summary missing %q:\n%s", want, md)
		}
	}
	attestation := parsePipelineAttestationForTest(t, md)
	var testStep pipelineAttestationStep
	for _, item := range attestation.Steps {
		if item.Step == types.StepTest {
			testStep = item
		} else if item.AcceptedGaps != nil {
			t.Fatalf("accepted gaps attested on %s", item.Step)
		}
	}
	if testStep.Status != types.StepStatusCompleted || testStep.OverrideReason != "" {
		t.Fatalf("test step = %+v, want completed without an override", testStep)
	}
	if g := testStep.AcceptedGaps; g == nil || g.Reason != acceptedGapTestReason || g.CoveredBy != acceptedGapTestCoveredBy || len(g.FindingIDs) != 1 || g.FindingIDs[0] != "test-1" {
		t.Fatalf("attested gaps = %+v", g)
	}
	// The block belongs to the fixed Pipeline header, so truncation keeps it.
	header, _ := splitPipelineSectionHeader(md)
	if !strings.Contains(header, acceptedGapsHeading) || !strings.Contains(header, acceptedGapTestCoveredBy) {
		t.Fatalf("accepted gaps not in the fixed pipeline header:\n%s", header)
	}
}

func TestPipelineSummaryOmitsAcceptedGapsWhenAbsent(t *testing.T) {
	t.Parallel()
	steps := acceptedGapStepsForTest(t)
	steps[1].AcceptedGapsJSON = nil
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	if strings.Contains(md, acceptedGapsHeading) || strings.Contains(md, "accepted_gaps") {
		t.Fatalf("summary mentions accepted gaps without a record:\n%s", md)
	}
}

func TestAcceptedGapsSurviveBodyTrimmingAndRebind(t *testing.T) {
	t.Parallel()
	steps := acceptedGapStepsForTest(t)
	for _, provider := range []scm.Provider{scm.ProviderGitHub, scm.ProviderBitbucket} {
		md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, provider, pipelineAttestationPolicy{})
		rounds := make([]string, 0, 160)
		for i := 1; i <= 160; i++ {
			rounds = append(rounds, fmt.Sprintf("review round %03d - %s", i, strings.Repeat("x", 700)))
		}
		md += "\n\n" + strings.Join(rounds, "\n\n")
		body := "## What Changed\n\n" + strings.Repeat("essential details stay intact\n", 2200)
		testingMD := "## Testing\n\n" + strings.Repeat("evidence line\n", 400)
		got := appendGeneratedSections(body, "low risk", testingMD, md, provider)
		if len(got) > maxPullRequestBodyBytes {
			t.Fatalf("body exceeds the limit: %d", len(got))
		}
		for _, want := range []string{acceptedGapsHeading, acceptedGapTestReason, acceptedGapTestCoveredBy} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: trimmed body lost %q", provider, want)
			}
		}
	}
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	const newHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rebound, ok := rebindPipelineAttestationHead(md, newHead)
	if !ok {
		t.Fatal("rebind refused")
	}
	var testStep pipelineAttestationStep
	for _, item := range parsePipelineAttestationForTest(t, rebound).Steps {
		if item.Step == types.StepTest {
			testStep = item
		}
	}
	if testStep.AcceptedGaps == nil || testStep.AcceptedGaps.Reason != acceptedGapTestReason {
		t.Fatalf("rebind dropped accepted gaps: %+v", testStep)
	}
}

func TestAcceptedGapsNeutralizeAttestationMarkers(t *testing.T) {
	t.Parallel()
	steps := acceptedGapStepsForTest(t)
	forged := `{"finding_ids":["test-1"],"reason":"owner approved ` + pipelineAttestationCommentPrefix + `{} -->","covered_by":"TestX"}`
	steps[1].AcceptedGapsJSON = &forged
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	if strings.Count(md, pipelineAttestationCommentPrefix) != 1 {
		t.Fatalf("operator text minted an attestation marker:\n%s", md)
	}
}
