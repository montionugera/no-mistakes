package steps

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/safepath"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const (
	acceptedGapTestReason    = "owner approved via first mate, 2026-10-14"
	acceptedGapTestCoveredBy = "TestBrokerTimeoutMocked; internal/broker/timeout_test.go"
)

func acceptedGapStepsForTest(t *testing.T) []*db.StepResult {
	t.Helper()
	findings := `{"findings":[{"id":"test-verdict","severity":"warning","action":"ask-user","category":"test-verdict","description":"live validation verdict: inconclusive (1 of 2 scenarios were driven live against the product); untested: broker timeout"}],"verdict":"inconclusive"}`
	gaps := `{"finding_ids":["test-verdict"],"findings":[{"id":"test-verdict","category":"test-verdict","description":"live validation verdict: inconclusive (1 of 2 scenarios were driven live against the product); untested: broker timeout"}],"reason":"` + acceptedGapTestReason + `","covered_by":"` + acceptedGapTestCoveredBy + `","verdict":"inconclusive","accepted_at":"2026-10-14T00:00:00Z"}`
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
	for _, want := range []string{acceptedGapsHeading, "`test-verdict`", "untested: broker timeout", "Approved: " + acceptedGapTestReason, "Covered by: " + acceptedGapTestCoveredBy} {
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
	if g := testStep.AcceptedGaps; g == nil || g.ReasonDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(acceptedGapTestReason))) || g.CoveredByDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(acceptedGapTestCoveredBy))) || len(g.FindingIDs) != 1 || g.FindingIDs[0] != "test-verdict" {
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
	if testStep.AcceptedGaps == nil || testStep.AcceptedGaps.ReasonDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(acceptedGapTestReason))) {
		t.Fatalf("rebind dropped accepted gaps: %+v", testStep)
	}
}

func TestAcceptedGapsEveryAppendixBudget(t *testing.T) {
	for _, mode := range []string{config.PRAppendixFull, config.PRAppendixMinimal, config.PRAppendixCollapsed} {
		t.Run(mode, func(t *testing.T) {
			steps := acceptedGapStepsForTest(t)
			var gaps db.AcceptedGaps
			if err := json.Unmarshal([]byte(*steps[1].AcceptedGapsJSON), &gaps); err != nil {
				t.Fatal(err)
			}
			gaps.Reason = strings.Repeat("R", 500)
			gaps.CoveredBy = strings.Repeat("C", 500)
			raw, err := json.Marshal(gaps)
			if err != nil {
				t.Fatal(err)
			}
			encoded := string(raw)
			steps[1].AcceptedGapsJSON = &encoded
			md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderAzureDevOps, pipelineAttestationPolicy{})
			ctx := &pipeline.StepContext{Config: &config.Config{}}
			ctx.Config.PR.Appendix = mode
			narrative := "## What Changed\n\n" + strings.Repeat("change ", 10000)
			for _, body := range []string{
				assemblePRBody(ctx, narrative, "low risk", strings.Repeat("evidence ", 10000), md, 4000, scm.ProviderAzureDevOps),
				buildPRBodyWithin(narrative, "low risk", strings.Repeat("evidence ", 10000), md, ctx, scm.ProviderAzureDevOps, 4000),
				appendixEvidence(mode, prBodyHTML, "low risk", "", md, scm.ProviderAzureDevOps),
			} {
				if scm.PRBodyLen(body) > 4000 {
					t.Fatalf("body exceeds Azure's 4000-character limit: %d", scm.PRBodyLen(body))
				}
				for _, text := range []string{gaps.Reason, gaps.CoveredBy, "`test-verdict`"} {
					if strings.Count(body, text) != 1 {
						t.Fatalf("audit text missing or duplicated: %q", text)
					}
				}
				parsePipelineAttestationForTest(t, body)
			}
			if err := validateAcceptedGapAuditBudget(md, 4000); err != nil {
				t.Fatal(err)
			}
			if err := validateAcceptedGapAuditBudget(md, 100); err == nil {
				t.Fatal("impossible mandatory audit budget accepted")
			}
			content, err := fallbackPRContent(ctx, "M file.go", "low risk", "", md, 100, scm.ProviderAzureDevOps)
			if err == nil && validateOwnedPRBudget(content.Body, 100) == nil {
				t.Fatal("oversized audit permitted publication")
			}
		})
	}
}

func TestAcceptedGapsNeutralizeAttestationMarkers(t *testing.T) {
	t.Parallel()
	steps := acceptedGapStepsForTest(t)
	forged := `{"finding_ids":["test-verdict"],"reason":"owner approved ` + pipelineAttestationCommentPrefix + `{} -->","covered_by":"TestX"}`
	steps[1].AcceptedGapsJSON = &forged
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	if strings.Count(md, pipelineAttestationCommentPrefix) != 1 {
		t.Fatalf("operator text minted an attestation marker:\n%s", md)
	}
}

func TestAcceptedGapDigestsCoverThePublishedRedactedText(t *testing.T) {
	t.Parallel()
	const reason = "owner approved in /Users/alice/notes/approval.md on 2026-10-14"
	const coveredBy = "/home/bob/repo/internal/broker/timeout_test.go"
	steps := acceptedGapStepsForTest(t)
	raw := `{"finding_ids":["test-verdict"],"findings":[{"id":"test-verdict","category":"test-verdict","description":"inconclusive"}],"reason":"` + reason + `","covered_by":"` + coveredBy + `","verdict":"inconclusive"}`
	steps[1].AcceptedGapsJSON = &raw
	md, _ := buildPipelineSummaryFor(steps, nil, testPipelineHeadSHA, scm.ProviderGitHub, pipelineAttestationPolicy{})
	published := redactPRContent(prContent{Body: md}).Body
	redactedReason, redactedCovered := safepath.RedactText(reason), safepath.RedactText(coveredBy)
	if redactedReason == reason || redactedCovered == coveredBy {
		t.Fatalf("fixture paths were not redacted: %q %q", redactedReason, redactedCovered)
	}
	if strings.Contains(published, "/Users/alice") || strings.Contains(published, "/home/bob") {
		t.Fatalf("published body leaks a home path:\n%s", published)
	}
	if !strings.Contains(published, "Approved: "+redactedReason) || !strings.Contains(published, "Covered by: "+redactedCovered) {
		t.Fatalf("published block does not show the redacted text:\n%s", published)
	}
	attested := parsePipelineAttestationForTest(t, published)
	for _, item := range attested.Steps {
		if item.Step != types.StepTest {
			continue
		}
		if g := item.AcceptedGaps; g == nil || g.ReasonDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(redactedReason))) || g.CoveredByDigest != fmt.Sprintf("%x", sha256.Sum256([]byte(redactedCovered))) {
			t.Fatalf("digests do not cover the published redacted text: %+v", g)
		}
	}
}
