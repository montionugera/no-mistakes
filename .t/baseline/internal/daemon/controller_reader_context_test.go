package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// This optional proof compiles Controller's real pure Swift contract/transport.
// Its explicit fixture has only bootstrap/read counters, no credential APIs.
func TestControllerSwiftReaderAuthenticatesOuterAndNestedPeers(t *testing.T) {
	sources := os.Getenv("NM_TEST_CONTROLLER_READER_SOURCES")
	if sources == "" {
		t.Skip("set NM_TEST_CONTROLLER_READER_SOURCES to Controller's reviewed .github/scripts")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the protected reader uses the reviewed Darwin Unix transport")
	}
	if !filepath.IsAbs(sources) {
		t.Fatal("Controller reader source directory must be absolute")
	}
	fixture := filepath.Join(t.TempDir(), "public-reader-context-fixture")
	compile := exec.Command("/usr/bin/swiftc",
		filepath.Join(sources, "op-dev-local-read-contract.swift"),
		filepath.Join(sources, "op-dev-local-reader-transport.swift"),
		filepath.Join(sources, "op-dev-local-reader-context-fixture.swift"), "-o", fixture)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile public Swift fixture: %v %s", err, output)
	}

	dedicated := startControllerPeerFixtureRoot(t)
	retained := startControllerPeerFixtureRoot(t)
	roots := []controllerPeerFixtureRoot{dedicated, retained}
	root := dedicated.paths.Root()
	classify := func(owner controllerPeerFixtureRoot) ipc.GateContextResult {
		t.Helper()
		client, err := ipc.Dial(owner.paths.Socket())
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		var result ipc.GateContextResult
		if err := client.Call(ipc.MethodGateContext, &ipc.GateContextParams{CWD: root}, &result); err != nil {
			t.Fatalf("authenticated native context RPC: %v", err)
		}
		return result
	}
	invoke := func(mode, expected string) {
		t.Helper()
		command := exec.Command(fixture, root, retained.paths.Root(), mode)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != expected {
			t.Fatalf("actual Swift %s peer: %v %s", mode, err, output)
		}
	}
	for _, owner := range roots {
		if result := classify(owner); result.Nested || result.AgentDescendant || result.DaemonDescendant || result.ManagedGit {
			t.Fatalf("ordinary native peer was not outer: %+v", result)
		}
	}
	invoke("outer", "outer-context-reached-bootstrap-sentinel")
	for index, owner := range roots {
		release, runID := trackControllerFixtureAgent(t, owner)
		if result := classify(owner); !result.Nested || !result.AgentDescendant || result.DaemonDescendant || result.RunID != runID || result.Phase != types.StepReview {
			t.Fatalf("root %d tracked peer not attributed: %+v", index, result)
		}
		other := roots[1-index]
		if result := classify(other); result.Nested {
			t.Fatalf("other root unexpectedly nested: %+v", result)
		}
		invoke("nested", "context-refused-before-bootstrap")
		release()
	}
	invoke("outer", "outer-context-reached-bootstrap-sentinel")
}

type controllerPeerFixtureRoot struct {
	paths    *paths.Paths
	database *db.DB
}

// Separate child daemons ensure the test process and its Node/Swift children
// are ordinary peers until this test explicitly records their agent ancestry.
func startControllerPeerFixtureRoot(t *testing.T) controllerPeerFixtureRoot {
	t.Helper()
	root, err := os.MkdirTemp("", "nm-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	p := paths.WithRoot(root)
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(t.TempDir(), "fixture-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec env -0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("NM_TEST_START_DAEMON", "1")
	t.Setenv("NM_DAEMON_HELPER_PROCESS", "daemon")
	t.Setenv("NM_TEST_DAEMON_START_TIMEOUT", "30s")
	t.Setenv("NM_TEST_DAEMON_START_POLL_INTERVAL", "10ms")
	if err := startDetachedDaemon(p); err != nil {
		t.Fatalf("start isolated native daemon: %v", err)
	}
	pid, err := ReadPID(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownIsolatedDaemon(t, p, pid) })
	d, err := db.Open(p.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return controllerPeerFixtureRoot{paths: p, database: d}
}

func trackControllerFixtureAgent(t *testing.T, owner controllerPeerFixtureRoot) (func(), string) {
	t.Helper()
	d := owner.database
	repo, err := d.InsertRepo(owner.paths.Root(), "https://github.com/owner/public-fixture.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	run, err := d.InsertRun(repo.ID, "feature", head, head)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
		t.Fatal(err)
	}
	step, err := d.InsertStepResult(run.ID, types.StepReview)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateStepStatus(step.ID, types.StepStatusRunning); err != nil {
		t.Fatal(err)
	}
	agentPID := os.Getpid()
	if err := d.SetStepAgentActivity(step.ID, "controlled public fixture ancestor", &agentPID); err != nil {
		t.Fatal(err)
	}
	release := func() {
		if err := d.SetStepAgentActivity(step.ID, "released fixture agent", nil); err != nil {
			t.Error(err)
		}
		if err := d.UpdateRunStatus(run.ID, types.RunFailed); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return release, run.ID
}
