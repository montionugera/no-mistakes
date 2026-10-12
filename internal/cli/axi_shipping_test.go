package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippingRunRejectsUnknownModeBeforeOpeningResources(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"axi", "run", "--shipping-mode", "skip-ci", "--intent", "goal"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("unknown shipping mode accepted")
	}
	if !bytes.Contains(out.Bytes(), []byte("shipping mode must be full or immutable-source")) {
		t.Fatalf("mode validation missing: %s %v", out.String(), err)
	}
}

func TestShippingStatusExposesNativeDurableIdentity(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo, _ := d.InsertRepo("/project", "https://github.com/owner/project.git", "main")
	head := strings.Repeat("a", 40)
	run, err := d.InsertRunWithShipping(repo.ID, "feature", head, head, nil, "", "", "", "main", false, nil, &types.ShippingOptions{Mode: types.ShippingFull, TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.CaptureReviewComparison(run.ID, db.ReviewComparison{SourceRepo: "owner/project", SourceBranch: "feature", TargetBranch: "main", TargetSHA: head, HeadSHA: head, MergeBaseSHA: head, DiffDigest: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetRunCIReadyWithReason(run.ID, true, false); err != nil {
		t.Fatal(err)
	}
	run, _ = d.GetRun(run.ID)
	out := axiDoc(runObjectField(runViewFromDB(run, nil, d)))
	for _, required := range []string{"shipping_mode: full", "review_generation: 1", "ci_ready: true", "source_repo: owner/project", "submitted_head_sha: " + head, "target_sha: " + head} {
		if !strings.Contains(out, required) {
			t.Fatalf("missing %q: %s", required, out)
		}
	}
}

func TestReviewReceiptRejectsUnknownPhaseBeforeResources(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"axi", "review-receipt", "--run", "01M4F7A4RJ1P22X7SX9SE6TVTQ", "--phase", "terminal"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("unknown phase accepted")
	}
	if !bytes.Contains(out.Bytes(), []byte("receipt phase must be review or ci-ready")) {
		t.Fatalf("phase validation missing: %s %v", out.String(), err)
	}
}

type trackedSigningReader struct{ reads int }

func (r *trackedSigningReader) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestReviewReceiptNestedCallerRefusesBeforeReadingSigningSeed(t *testing.T) {
	root := makeSocketSafeTempDir(t)
	t.Setenv("NM_HOME", root)
	p := paths.WithRoot(root)
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer()
	srv.Handle(ipc.MethodHealth, func(context.Context, json.RawMessage) (interface{}, error) {
		return &ipc.HealthResult{Status: "ok"}, nil
	})
	srv.Handle(ipc.MethodGateContext, func(context.Context, json.RawMessage) (interface{}, error) {
		return &ipc.GateContextResult{Nested: true, AgentDescendant: true, RunID: "enclosing-run", Phase: types.StepReview}, nil
	})
	startIPCServer(t, srv, p.Socket())
	client := dialReady(t, p.Socket())
	client.Close()
	cmd := newAxiReviewReceiptCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	reader := &trackedSigningReader{}
	cmd.SetIn(reader)
	cmd.SetArgs([]string{"--run", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--phase", "review", "--signing-key-stdin"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("nested signing caller accepted")
	}
	if reader.reads != 0 {
		t.Fatal("nested caller read signing seed")
	}
	if !strings.Contains(out.String(), "nested_gate_context") || strings.Contains(out.String(), `"signature"`) || strings.Contains(out.String(), `"payload"`) {
		t.Fatalf("nested refusal output: %s", out.String())
	}
}

func TestShippingStatusBeforeReviewUsesEnrollmentIdentityWithoutProof(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo, err := d.InsertRepo("/unavailable-project", "git@github.com:owner/project.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	run, err := d.InsertRunWithShipping(repo.ID, "refs/heads/feature", head, head, nil, "", "", "", "dev", false, nil, &types.ShippingOptions{Mode: types.ShippingFull, TargetBranch: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	out := axiDoc(runObjectField(runViewFromDB(run, nil, d)))
	for _, required := range []string{"source_repo: owner/project", "source_branch: feature", "target_branch: dev", "review_generation: null", "target_sha: null", "ci_ready: false"} {
		if !strings.Contains(out, required) {
			t.Fatalf("missing %q: %s", required, out)
		}
	}
	ipcOut := axiDoc(runObjectField(runViewFromIPC(&ipc.RunInfo{Shipping: run.Shipping, Branch: run.Branch, HeadSHA: head, SourceRepo: "owner/project", SourceBranch: "feature", TargetBranch: "dev"})))
	for _, required := range []string{"source_repo: owner/project", "review_generation: null", "target_sha: null"} {
		if !strings.Contains(ipcOut, required) {
			t.Fatalf("IPC missing %q: %s", required, ipcOut)
		}
	}
}
