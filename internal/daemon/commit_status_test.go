package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/forgecontext"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/pipeline/steps"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"github.com/kunchenguid/no-mistakes/internal/worktrees"
)

func TestRecoverOnStartup_ReplacesPublishedSuccessWithFailure(t *testing.T) {
	for _, profileOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("profile_only_%v", profileOnly), func(t *testing.T) {
			p := paths.WithRoot(t.TempDir())
			if err := p.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(p.DB())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			repo, err := d.InsertRepoWithID("status-recovery", t.TempDir(), "https://github.com/test/repo", "main")
			if err != nil {
				t.Fatal(err)
			}
			run, err := d.InsertRun(repo.ID, "feature", "mutable-head", "base")
			if err != nil {
				t.Fatal(err)
			}
			if err := d.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
				t.Fatal(err)
			}
			if err := d.PutCommitStatusPublication(run.ID, db.CommitStatusPublication{SHA: "pushed-head", Host: "github.com", Repo: "test/repo", Validated: true}); err != nil {
				t.Fatal(err)
			}
			binDir := t.TempDir()
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, "gh"), "../pipeline/fakecli")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build fake gh: %v: %s", err, out)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_CLI_MODE", "gh")
			var mu sync.Mutex
			var states []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct{ State, Context string }
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.URL.Path != "/repos/test/repo/statuses/pushed-head" || payload.Context != "no-mistakes" {
					t.Errorf("request = %s %s %+v", r.Method, r.URL.Path, payload)
				}
				mu.Lock()
				states = append(states, payload.State)
				mu.Unlock()
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			t.Setenv("FAKE_CLI_STATUS_API_URL", server.URL)
			var forge *forgecontext.Context
			if profileOnly {
				profileDir := t.TempDir()
				if err := os.WriteFile(filepath.Join(profileDir, "hosts.yml"), []byte("github.com:\n  user: profile-user\n  oauth_token: synthetic-profile-token\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p.ConfigFile(), []byte(fmt.Sprintf("forge_profiles:\n  github.com:\n    gh_config_dir: %q\n    expected_login: profile-user\n", profileDir)), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GH_CONFIG_DIR", t.TempDir())
				for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_REPO"} {
					t.Setenv(key, "synthetic-ambient-value")
				}
				t.Setenv("FAKE_CLI_STATUS_REQUIRED_GH_CONFIG_DIR", profileDir)
				global, err := config.LoadGlobal(p.ConfigFile())
				if err != nil {
					t.Fatal(err)
				}
				forge, err = forgecontext.Resolve(context.Background(), global.ForgeProfiles, repo.UpstreamURL, repo.ForkURL)
				if err != nil {
					t.Fatal(err)
				}
			}
			(&steps.PushStep{}).ReportPublishedHeadStatus(&pipeline.StepContext{Ctx: context.Background(), DB: d, Run: run, ForgeContext: forge}, "success", "")
			publication, err := d.GetCommitStatusPublication(run.ID)
			if err != nil || publication.LastState != "success" {
				t.Fatalf("initial publication = %+v, %v", publication, err)
			}
			mgr := NewRunManager(d, p, nil)
			recoveredRuns := recoverOnStartup(d, p, mgr, worktrees.New(p, nil))
			reportRecoveredCommitStatuses(context.Background(), d, p, mgr, recoveredRuns, time.Second)
			recovered, err := d.GetRun(run.ID)
			if err != nil || recovered.Status != types.RunFailed {
				t.Fatalf("recovered run = %+v, %v", recovered, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(states) != 2 || states[0] != "success" || states[1] != "failure" {
				t.Fatalf("states = %v, want success then failure", states)
			}
		})
	}
}

func TestStatusRecoveryBounds(t *testing.T) {
	for _, scenario := range []string{"deadline", "ipc"} {
		t.Run(scenario, func(t *testing.T) {
			p := paths.WithRoot(t.TempDir())
			if err := p.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(p.DB())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			repo, err := d.InsertRepoWithID("slow-status", t.TempDir(), "https://github.com/test/repo", "main")
			if err != nil {
				t.Fatal(err)
			}
			var runs []*db.Run
			for i := 0; i < 10; i++ {
				run, err := d.InsertRun(repo.ID, fmt.Sprintf("feature-%d", i), "head", "base")
				if err != nil {
					t.Fatal(err)
				}
				if err := d.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
					t.Fatal(err)
				}
				if err := d.PutCommitStatusPublication(run.ID, db.CommitStatusPublication{SHA: "pushed-head", Host: "github.com", Repo: "test/repo", Validated: true, LastState: "success"}); err != nil {
					t.Fatal(err)
				}
				runs = append(runs, run)
			}
			binDir := t.TempDir()
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, "gh"), "../pipeline/fakecli")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build fake gh: %v: %s", err, out)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_CLI_MODE", "gh")
			requested := make(chan struct{}, 10)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct{ State, Context string }
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.URL.Path != "/repos/test/repo/statuses/pushed-head" || payload.State != "failure" || payload.Context != "no-mistakes" {
					t.Errorf("request = %s %s %+v", r.Method, r.URL.Path, payload)
				}
				requested <- struct{}{}
				<-release
			}))
			defer server.Close()
			defer close(release)
			t.Setenv("FAKE_CLI_STATUS_API_URL", server.URL)
			if scenario == "deadline" {
				mgr := NewRunManager(d, p, nil)
				for _, run := range runs {
					if err := d.UpdateRunStatus(run.ID, types.RunFailed); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					reportRecoveredCommitStatuses(context.Background(), d, p, mgr, runs, 500*time.Millisecond)
				}()
				select {
				case <-requested:
				case <-time.After(3 * time.Second):
					t.Fatal("recovery never reached status endpoint")
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("reporter exceeded its overall deadline")
				}
				if len(requested) != 0 {
					t.Fatal("reporter continued posting after its overall deadline")
				}
			} else {
				exited := make(chan error, 1)
				go func() { exited <- RunWithResources(p, d) }()
				t.Cleanup(func() {
					if client, err := ipc.Dial(p.Socket()); err == nil {
						_ = client.Call(ipc.MethodShutdown, &ipc.ShutdownParams{}, nil)
						client.Close()
					}
				})
				waitForDaemonReadyOrExit(t, p, exited)
				select {
				case <-requested:
				case <-time.After(3 * time.Second):
					t.Fatal("recovery never reached status endpoint")
				}
				client, err := ipc.Dial(p.Socket())
				if err != nil {
					t.Fatal(err)
				}
				var health ipc.HealthResult
				if err := client.CallWithTimeout(ipc.MethodHealth, &ipc.HealthParams{}, &health, time.Second); err != nil {
					t.Fatal(err)
				}
				if err := client.Call(ipc.MethodShutdown, &ipc.ShutdownParams{}, nil); err != nil {
					t.Fatal(err)
				}
				client.Close()
				select {
				case err := <-exited:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("shutdown did not cancel and join recovery reporter")
				}
			}
			for _, run := range runs {
				publication, err := d.GetCommitStatusPublication(run.ID)
				if err != nil || publication == nil || publication.LastState != "success" {
					t.Fatalf("failed posting must preserve the last posted state: %+v, %v", publication, err)
				}
			}
		})
	}
}
