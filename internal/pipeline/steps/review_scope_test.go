package steps

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
)

// This executes the generated reviewer protocol and its output transport.
// The oracle is the existing id/reason withdrawal contract, including a
// withdrawal delivered by the focused coverage turn rather than the first.
func TestReviewStep_ScopeDispositionRetractionProtocol(t *testing.T) {
	for _, completion := range []bool{false, true} {
		name := "full review"
		if completion {
			name = "coverage completion"
		}
		t.Run(name, func(t *testing.T) {
			dir, base, head := twoFileRepo(t)
			ag := &mockAgent{}
			ag.runFn = func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
				var schema struct {
					Properties map[string]struct {
						Type  string `json:"type"`
						Items struct {
							Required   []string `json:"required"`
							Properties map[string]struct {
								Type string `json:"type"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"properties"`
				}
				if err := json.Unmarshal(opts.JSONSchema, &schema); err != nil {
					return nil, err
				}
				property := schema.Properties["withdrawn_findings"]
				if property.Type != "array" || property.Items.Properties["id"].Type != "string" || property.Items.Properties["reason"].Type != "string" || len(property.Items.Required) != 2 {
					t.Errorf("emitted protocol omitted the independent id/reason retraction: %+v", property)
				}
				payload := `{"findings":[],"risk_level":"low","risk_rationale":"clean","risk_scope":"source-or-external","reviewed_paths":["feature.txt","feature2.txt"],"withdrawn_findings":[{"id":"review-1","reason":"Independent trace confirms owner requirements exclude this remedy"}]}`
				if completion && opts.Purpose == "review" {
					payload = coverageFindingJSON([]string{"feature.txt"})
				}
				return &agent.Result{Output: json.RawMessage(payload)}, nil
			}
			sctx := newTestContextWithDBRecords(t, ag, dir, base, head, config.Commands{})
			sctx.ScopeFindings = `{"findings":[{"id":"review-1","severity":"error","file":"feature.txt","description":"declined scope claim","action":"ask-user"}]}`
			outcome, err := (&ReviewStep{}).Execute(sctx)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.NeedsApproval || len(outcome.WithdrawnFindings) != 1 || outcome.WithdrawnFindings[0].ID != "review-1" || outcome.WithdrawnFindings[0].Reason == "" || len(outcome.ReviewedPaths) != 2 {
				t.Fatalf("independent adjudication or its coverage was lost: %+v", outcome)
			}
			wantCalls := 1
			if completion {
				wantCalls = 2
			}
			if len(ag.calls) != wantCalls {
				t.Fatalf("review invocations = %d, want %d", len(ag.calls), wantCalls)
			}
		})
	}
}
