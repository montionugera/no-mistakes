package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/agentcfg"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline/steps"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Drive startup recovery through the running daemon and its IPC interface.
// The oracle is the trust contract: a parked run survives a sibling's tracking
// lock, but an unavailable or newly invalid trusted tree cannot use stale data.
// All pipeline steps are production steps; the parked review is never released.
func TestTrustedConfigRecoveryLive(t *testing.T) {
	for _, mode := range []string{"locked-tracking-ref", "unavailable-origin", "fresh-invalid-config"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("", "d")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			p := paths.WithRoot(root)
			if err := p.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(p.DB())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			// Recovery constructs the adapter but does not invoke it while parked.
			claude := writeMockClaude(t, t.TempDir())
			if err := os.WriteFile(p.ConfigFile(), []byte("agent: claude\nagent_path_override:\n  claude: "+claude+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			repo, head := setupTestGitRepo(t, p, d, "trusted-live")
			bare := p.RepoDir(repo.ID)
			gitCmd(t, bare, "fetch", "origin", "main:refs/remotes/origin/main")
			fetchHead := filepath.Join(bare, "FETCH_HEAD")
			before, err := os.ReadFile(fetchHead)
			if err != nil {
				t.Fatal(err)
			}
			run, err := d.InsertRun(repo.ID, "feature/recovery", head, head)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, bare, "worktree", "add", "--detach", p.WorktreeDir(repo.ID, run.ID), head)
			parkRunAtReviewGate(t, d, run.ID, steps.AllSteps())
			if err := d.SetRunAwaitingAgent(run.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "unavailable-origin" {
				gitCmd(t, bare, "remote", "set-url", "origin", filepath.Join(root, "missing-origin"))
			} else {
				if mode == "fresh-invalid-config" {
					commitDefaultBranchConfig(t, repo.WorkingPath, "agent: invalid-agent\n")
				}
				if err := os.WriteFile(filepath.Join(bare, "refs", "remotes", "origin", "main.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runTestDaemon(t, p, d, nil, 35*time.Second)
			client, err := ipc.Dial(p.Socket())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var result ipc.GetRunResult
			if err := client.Call(ipc.MethodGetRun, &ipc.GetRunParams{RunID: run.ID}, &result); err != nil {
				t.Fatal(err)
			}
			if mode == "locked-tracking-ref" {
				if result.Run.Status != types.RunRunning || result.Run.AwaitingAgentSince == nil {
					t.Fatalf("parked run lost during locked-ref recovery: %+v", result.Run)
				}
			} else if result.Run.Status != types.RunFailed {
				t.Fatalf("unsafe recovery used stale trusted tree: %+v", result.Run)
			}
			if got := gitOutput(t, bare, "rev-parse", "refs/remotes/origin/main"); got != head {
				t.Fatalf("shared tracking ref moved: %s, want %s", got, head)
			}
			after, err := os.ReadFile(fetchHead)
			if err != nil || string(after) != string(before) {
				t.Fatalf("FETCH_HEAD changed: %q, %v", after, err)
			}
			refs, err := git.Run(context.Background(), bare, "for-each-ref", "--format=%(refname)", "refs/no-mistakes/trusted-config/")
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(refs) != "" {
				t.Fatalf("private refs leaked: %s", refs)
			}
			t.Logf("IPC run status=%s; parked=%v; tracking SHA=%s; FETCH_HEAD preserved; private refs removed", result.Run.Status, result.Run.AwaitingAgentSince != nil, head)
		})
	}
}

// The daemon must read freshly fetched trusted config before accepting a new
// run or a Pi pin. Refusals must preserve any existing validation on the branch.
func TestTrustedConfigStartupRefusalLive(t *testing.T) {
	for _, mode := range []string{"unavailable-origin", "fresh-agent", "pi-trusted-agent", "pi-unavailable-origin"} {
		t.Run(mode, func(t *testing.T) {
			p, d := startTestDaemonWithSteps(t, nil)
			repo, head := setupTestGitRepo(t, p, d, "startup-live")
			bare := p.RepoDir(repo.ID)
			gitCmd(t, bare, "fetch", "origin", "main:refs/remotes/origin/main")
			fetchHead := filepath.Join(bare, "FETCH_HEAD")
			before, err := os.ReadFile(fetchHead)
			if err != nil {
				t.Fatal(err)
			}
			isPi := strings.HasPrefix(mode, "pi-")
			params := &ipc.PushReceivedParams{Gate: bare, Ref: "refs/heads/feature/new", Old: strings.Repeat("0", 40), New: head}
			var active *db.Run
			if isPi {
				if err := os.WriteFile(p.ConfigFile(), []byte("agent: pi\n"), 0600); err != nil {
					t.Fatal(err)
				}
				params.PiProfile = &agentcfg.PiProfile{Model: "openai-codex/gpt-5.4", Effort: agentcfg.EffortHigh}
				active, err = d.InsertRun(repo.ID, "feature/new", head, head)
				if err != nil {
					t.Fatal(err)
				}
				if err := d.UpdateRunStatus(active.ID, types.RunRunning); err != nil {
					t.Fatal(err)
				}
			}
			wantError := "failed to fetch or resolve trusted default branch"
			if strings.Contains(mode, "unavailable-origin") {
				gitCmd(t, bare, "remote", "set-url", "origin", filepath.Join(p.Root(), "missing-origin"))
				if isPi {
					wantError = "failed to fetch trusted default branch"
				}
			} else {
				yaml := "agent: invalid-agent\n"
				wantError = "invalid-agent"
				if isPi {
					yaml = "agent: claude\n"
					wantError = "Pi run profile requires agent: pi without non-Pi fallbacks"
				}
				commitDefaultBranchConfig(t, repo.WorkingPath, yaml)
				if err := os.WriteFile(filepath.Join(bare, "refs", "remotes", "origin", "main.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			client, err := ipc.Dial(p.Socket())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var response ipc.PushReceivedResult
			err = client.Call(ipc.MethodPushReceived, params, &response)
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("startup refusal = %v; want %s", err, wantError)
			}
			t.Logf("push_received refusal: %s", err)
			if isPi {
				runs, err := d.GetRunsByRepo(repo.ID)
				if err != nil || len(runs) != 1 || runs[0].ID != active.ID || runs[0].Status != types.RunRunning {
					t.Fatalf("refused pin altered existing validation: %+v, %v", runs, err)
				}
				t.Log("refused Pi pin preserved the existing running validation")
			}
			if got := gitOutput(t, bare, "rev-parse", "refs/remotes/origin/main"); got != head {
				t.Fatalf("shared tracking ref moved: %s, want %s", got, head)
			}
			after, err := os.ReadFile(fetchHead)
			if err != nil || string(after) != string(before) {
				t.Fatalf("FETCH_HEAD changed: %q, %v", after, err)
			}
			refs, err := git.Run(context.Background(), bare, "for-each-ref", "--format=%(refname)", "refs/no-mistakes/trusted-config/")
			if err != nil || strings.TrimSpace(refs) != "" {
				t.Fatalf("private refs leaked: %q, %v", refs, err)
			}
			t.Logf("tracking SHA=%s; FETCH_HEAD preserved; private refs removed", head)
		})
	}
}
