package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const (
	acceptGapReason    = "owner approved via first mate, 2026-10-14"
	acceptGapCoveredBy = "TestBrokerTimeoutMocked; internal/broker/timeout_test.go"
)

func TestAxiRespond_AcceptGapFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"covered-by without accept-gap", []string{"--action", "approve", "--step", "test", "--reason", acceptGapReason, "--covered-by", "TestX"}, "--covered-by applies only with --accept-gap"},
		{"fix action", []string{"--action", "fix", "--findings", "test-1", "--step", "test", "--accept-gap", "test-1", "--reason", acceptGapReason, "--covered-by", "TestX"}, "--accept-gap applies only to --action approve"},
		{"skip action", []string{"--action", "skip", "--step", "test", "--accept-gap", "test-1", "--reason", acceptGapReason, "--covered-by", "TestX"}, "--accept-gap applies only to --action approve"},
		{"missing reason", []string{"--action", "approve", "--step", "test", "--accept-gap", "test-1", "--covered-by", "TestX"}, "--reason of at least 10 characters"},
		{"short reason", []string{"--action", "approve", "--step", "test", "--accept-gap", "test-1", "--reason", "ok", "--covered-by", "TestX"}, "--reason of at least 10 characters"},
		{"missing covered-by", []string{"--action", "approve", "--step", "test", "--accept-gap", "test-1", "--reason", acceptGapReason}, "non-empty --covered-by"},
		{"blank covered-by", []string{"--action", "approve", "--step", "test", "--accept-gap", "test-1", "--reason", acceptGapReason, "--covered-by", "  "}, "non-empty --covered-by"},
		{"multiline reason", []string{"--action", "approve", "--step", "test", "--accept-gap", "test-1", "--reason", "owner approved\nvia first mate", "--covered-by", "TestX"}, "single line"},
		{"no ids", []string{"--action", "approve", "--step", "test", "--accept-gap", " , ", "--reason", acceptGapReason, "--covered-by", "TestX"}, "at least one finding id"},
		{"not the Test step", []string{"--action", "approve", "--step", "review", "--accept-gap", "test-1", "--reason", acceptGapReason, "--covered-by", "TestX"}, "applies only to the Test step"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var responded atomic.Bool
			fx := newAxiTimeoutFixture(t, axiTimeoutOpts{
				respond: func(context.Context, json.RawMessage) (interface{}, error) {
					responded.Store(true)
					return &ipc.RespondResult{OK: true}, nil
				},
			})
			fx.setGetRun(func(context.Context, int) (*ipc.RunInfo, error) { return fx.awaiting(), nil })
			out, err := executeCmd(append([]string{"axi", "respond", "--wait", "3s"}, tt.args...)...)
			var ee *exitError
			if !errors.As(err, &ee) || ee.code != 2 {
				t.Fatalf("error = %v, want usage exit 2\n%s", err, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("output missing %q:\n%s", tt.want, out)
			}
			if responded.Load() {
				t.Fatal("an invalid accepted-gap response reached the daemon")
			}
		})
	}
}

func TestAxiRespond_ForwardsAnAcceptedGapVerbatim(t *testing.T) {
	var got ipc.RespondParams
	var responded atomic.Bool
	fx := newAxiTimeoutFixture(t, axiTimeoutOpts{
		respond: func(_ context.Context, raw json.RawMessage) (interface{}, error) {
			if err := json.Unmarshal(raw, &got); err != nil {
				return nil, err
			}
			responded.Store(true)
			return &ipc.RespondResult{OK: true}, nil
		},
	})
	parked := func() *ipc.RunInfo {
		run := fx.awaiting()
		run.Steps[0].StepName = types.StepTest
		return run
	}
	fx.setGetRun(func(context.Context, int) (*ipc.RunInfo, error) {
		if responded.Load() {
			return fx.completed(), nil
		}
		return parked(), nil
	})
	out, err := executeCmd("axi", "respond", "--action", "approve", "--accept-gap", "test-1, test-2", "--reason", acceptGapReason, "--covered-by", acceptGapCoveredBy, "--wait", "3s")
	if err != nil {
		t.Fatalf("accepted gap refused: %v\n%s", err, out)
	}
	if got.Action != types.ActionApprove || got.Step != types.StepTest || strings.Join(got.AcceptGaps, "|") != "test-1|test-2" || got.ApprovalReason != acceptGapReason || got.CoveredBy != acceptGapCoveredBy {
		t.Fatalf("forwarded params = %+v", got)
	}
}
