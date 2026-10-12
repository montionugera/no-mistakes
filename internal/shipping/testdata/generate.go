//go:build ignore

// Generate the deterministic, non-secret cross-language receipt fixture.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/kunchenguid/no-mistakes/internal/shipping"
	"os"
	"strings"
)

func main() {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	key := ed25519.NewKeyFromSeed(seed)
	findings := []shipping.Finding{{ID: "f1", Severity: "info", File: "source.go", Action: "no-op", Description: "Reviewed <source> & behavior\nNo change required.", Disposition: "non-actionable"}}
	rawFindings, _ := json.Marshal(findings)
	head := strings.Repeat("a", 40)
	target := strings.Repeat("b", 40)
	p := shipping.Payload{SchemaVersion: 1, RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Phase: "review", IssuedAt: 1791500000, SourceRepo: "vifar/controller", SourceBranch: "codex/fixture", SubmittedHeadSHA: head, LocalHeadSHA: head, ForgeHeadSHA: head, PRURL: "https://github.com/vifar/controller/pull/7", TargetBranch: "dev", TargetSHA: target, MergeBaseSHA: target, DiffDigest: strings.Repeat("c", 64), Generation: 1, IntentDigest: shipping.Digest([]byte("fixture intent\n")), ShippingMode: "full", Review: shipping.Review{Status: "completed", ApprovedHeadSHA: head, FindingsDigest: shipping.Digest(rawFindings), Findings: findings}}
	for _, name := range []string{"intent", "rebase", "review", "test", "document", "lint", "push", "pr", "ci"} {
		status := "completed"
		if name == "ci" {
			status = "running"
		}
		p.Steps = append(p.Steps, shipping.Step{Step: name, Status: status, Execution: "performed"})
	}
	payload, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	envelope, err := shipping.Sign(payload, strings.NewReader(base64.StdEncoding.EncodeToString(seed)))
	if err != nil {
		panic(err)
	}
	for name, data := range map[string][]byte{"review-payload.json": payload, "review-envelope.json": envelope, "review-public-key.txt": []byte(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))} {
		if err := os.WriteFile("internal/shipping/testdata/"+name, append(data, '\n'), 0644); err != nil {
			panic(err)
		}
	}
}
