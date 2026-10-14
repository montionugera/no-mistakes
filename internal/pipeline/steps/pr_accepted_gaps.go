package steps

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// acceptedGapsHeading opens the owner-accepted Test gap block. The block sits
// in the Pipeline section's fixed header (see splitPipelineSectionHeader), not
// in the Testing section: Testing is the first section the body budget drops,
// and an accepted gap is audit evidence that must survive every trimming path.
const acceptedGapsHeading = "### Accepted gaps"

const maxAcceptedGapDescriptionRunes = 500

// pipelineAttestationAcceptedGaps is the machine-readable form of an accepted
// Test gap, carried on the Test step of the attestation.
type pipelineAttestationAcceptedGaps struct {
	FindingIDs      []string `json:"finding_ids"`
	ReasonDigest    string   `json:"reason_sha256"`
	CoveredByDigest string   `json:"covered_by_sha256"`
}

func attestedAcceptedGaps(sr *db.StepResult) *pipelineAttestationAcceptedGaps {
	if sr == nil || sr.StepName != types.StepTest {
		return nil
	}
	gaps, err := sr.RecordedAcceptedGaps()
	if err != nil || gaps == nil {
		return nil
	}
	return &pipelineAttestationAcceptedGaps{FindingIDs: gaps.FindingIDs, ReasonDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(gaps.Reason))), CoveredByDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(gaps.CoveredBy)))}
}

func acceptedGapAuditBlock(pipelineMD string) string {
	header, _ := splitPipelineSectionHeader(pipelineMD)
	start := strings.Index(header, acceptedGapsHeading+"\n")
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(header[start:])
}

func validateAcceptedGapAuditBudget(pipelineMD string, limit int) error {
	tail := joinBlocks(noMistakesPRSignature, carriedAttestation(pipelineMD), acceptedGapAuditBlock(pipelineMD))
	if len(tail) > maxPullRequestBodyBytes || (limit > 0 && scm.PRBodyLen(tail) > limit) {
		return fmt.Errorf("mandatory pipeline attestation and accepted-gap audit block exceed PR body limit; refusing publication")
	}
	return nil
}

func preserveAcceptedGapAudit(body, prefix, pipelineMD string, limit int, units func(string) int, clamp func(string, int) string) string {
	block := acceptedGapAuditBlock(pipelineMD)
	if block == "" {
		return body
	}
	marker := carriedAttestation(pipelineMD)
	if strings.Contains(body, block) && (marker == "" || strings.Contains(body, marker)) && (limit <= 0 || units(body) <= limit) {
		return body
	}
	tail := joinBlocks(noMistakesPRSignature, marker, block)
	if limit <= 0 {
		return joinBlocks(body, block)
	}
	if units(tail) > limit {
		return tail
	}
	return shrinkMeasuredKeepingTail(joinBlocks(prefix, tail), tail, limit, units, clamp)
}

// buildAcceptedGapsBlock renders each accepted finding with its description,
// plus the approval reason and the covering tests verbatim. It returns "" when
// no completed Test step carries a recorded gap. The block is one paragraph
// (no blank lines) so the Pipeline header parser can keep it whole.
func buildAcceptedGapsBlock(steps []*db.StepResult, flavor prBodyFlavor) string {
	for _, sr := range steps {
		if sr == nil || sr.StepName != types.StepTest || sr.Status != types.StepStatusCompleted {
			continue
		}
		gaps, err := sr.RecordedAcceptedGaps()
		if err != nil || gaps == nil || len(gaps.FindingIDs) == 0 {
			continue
		}
		descriptions := make(map[string]string, len(gaps.Findings))
		for _, f := range gaps.Findings {
			descriptions[f.ID] = f.Description
		}
		var b strings.Builder
		b.WriteString(acceptedGapsHeading)
		b.WriteString("\n")
		for _, id := range gaps.FindingIDs {
			b.WriteString("- `")
			b.WriteString(strings.ReplaceAll(id, "`", "'"))
			b.WriteString("`")
			if description := singleLineText(descriptions[id], maxAcceptedGapDescriptionRunes); description != "" {
				b.WriteString(": ")
				b.WriteString(escapePRText(description, flavor))
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- Approved: %s\n", escapePRText(gaps.Reason, flavor))
		fmt.Fprintf(&b, "- Covered by: %s", escapePRText(gaps.CoveredBy, flavor))
		return b.String()
	}
	return ""
}

func singleLineText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return s
}
