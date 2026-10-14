package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/forgecontext"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type statusRequest struct{ Path, State, Context, Description, TargetURL string }

func TestPipeline_GitHubCommitStatuses(t *testing.T) {
	for _, scenario := range []string{"pass", "fail after push", "cancel after push", "no push", "forgejo", "bitbucket", "repair head", "posting fails", "already upstream", "fork", "formatter head", "recovered failure", "CI repair readiness", "Test no-go exception", "Test inconclusive exception", "skipped Test"} {
		t.Run(scenario, func(t *testing.T) {
			dir, base, head := setupGitRepo(t)
			remote := t.TempDir()
			gitCmd(t, remote, "init", "--bare", ".")
			gitCmd(t, dir, "config", "url."+remote+".insteadOf", "https://github.com/test/repo")
			sctx := newTestContextWithDBRecords(t, &mockAgent{name: "test"}, dir, base, head, config.Commands{})
			gitCmd(t, dir, "remote", "set-url", "origin", "https://github.com/test/repo")
			sctx.Repo.URLsVerified = true
			if scenario == "already upstream" {
				gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/feature")
			}
			if scenario == "fork" {
				sctx.Repo.ForkURL = "https://github.com/fork/repo"
				gitCmd(t, dir, "config", "url."+remote+".insteadOf", sctx.Repo.ForkURL)
			}
			if scenario == "formatter head" {
				sctx.Config.Commands.Format = "printf formatted > feature.txt"
			}

			var mu sync.Mutex
			var requests []statusRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					State       string `json:"state"`
					Context     string `json:"context"`
					Description string `json:"description"`
					TargetURL   string `json:"target_url"`
				}
				if r.Method != "POST" {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				mu.Lock()
				requests = append(requests, statusRequest{r.URL.Path, payload.State, payload.Context, payload.Description, payload.TargetURL})
				mu.Unlock()
				if scenario == "posting fails" {
					http.Error(w, "status service unavailable", 503)
					return
				}
				w.WriteHeader(201)
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			env, ghLog := fakeGH(t, "")
			env = append(env, "FAKE_CLI_STATUS_API_URL="+server.URL)
			for key, value := range environmentEntries(t, env) {
				t.Setenv(key, value)
			}
			provider := scm.ProviderGitHub
			if scenario == "forgejo" {
				provider = scm.ProviderForgejo
			}
			if scenario == "bitbucket" {
				provider = scm.ProviderBitbucket
			}
			var steps []pipeline.Step
			for _, name := range []types.StepName{types.StepReview, types.StepTest, types.StepDocument, types.StepLint} {
				name := name
				steps = append(steps, &transitionStep{name: name, execute: func(sc *pipeline.StepContext) (*pipeline.StepOutcome, error) {
					o := &pipeline.StepOutcome{}
					if name == types.StepTest {
						if scenario == "skipped Test" {
							o.Skipped = true
						}
						if strings.HasPrefix(scenario, "Test ") {
							verdict := strings.TrimSuffix(strings.TrimPrefix(scenario, "Test "), " exception")
							o.Findings = fmt.Sprintf(`{"findings":[],"verdict":%q}`, verdict)
							o.NeedsApproval = true
						}
					}
					if name == types.StepReview {
						o.ReviewApprovedHeadSHA = sc.Run.HeadSHA
					}
					return o, nil
				}})
			}
			if scenario != "no push" {
				steps = append(steps, &PushStep{})
			}
			uncertified := scenario == "skipped Test" || strings.HasPrefix(scenario, "Test ")
			ciCalls := 0
			repaired := ""
			steps = append(steps, &transitionStep{name: types.StepCI, execute: func(sc *pipeline.StepContext) (*pipeline.StepOutcome, error) {
				ciCalls++
				if ciCalls == 1 && scenario != "no push" && scenario != "forgejo" && scenario != "bitbucket" && scenario != "already upstream" {
					mu.Lock()
					n := len(requests)
					mu.Unlock()
					wantAtCI := 2
					if uncertified {
						wantAtCI = 1
					}
					if n != wantAtCI {
						t.Fatalf("requests at CI entry = %d, want pending then success before CI", n)
					}
				}
				if uncertified {
					sc.PublishedHeadValidated(head)
				}
				if scenario == "fail after push" {
					return nil, errors.New("CI failed")
				}
				if scenario == "cancel after push" {
					return nil, errors.New(types.RunCancelReasonAbortedByUser)
				}
				if (scenario == "repair head" || scenario == "CI repair readiness") && ciCalls == 1 {
					if err := os.WriteFile(filepath.Join(dir, "repair.txt"), []byte("repair"), 0600); err != nil {
						t.Fatal(err)
					}
					gitCmd(t, dir, "add", "repair.txt")
					gitCmd(t, dir, "commit", "-m", "repair")
					repaired = gitCmd(t, dir, "rev-parse", "HEAD")
					sc.Run.HeadSHA = repaired
					if err := sc.DB.UpdateRunHeadSHA(sc.Run.ID, repaired); err != nil {
						t.Fatal(err)
					}
					if scenario == "CI repair readiness" {
						if err := publishRunHead(sc, repaired, "", nil); err != nil {
							return nil, err
						}
						mu.Lock()
						before := len(requests)
						mu.Unlock()
						if before != 3 {
							t.Fatalf("requests before readiness = %d, want initial pending/success and repair pending", before)
						}
						sc.PublishedHeadValidated(head) // stale readiness must not certify the repair
						mu.Lock()
						afterStale := len(requests)
						mu.Unlock()
						if afterStale != before {
							t.Fatal("stale head certified repair")
						}
						sc.PublishedHeadValidated(repaired)
						return &pipeline.StepOutcome{}, nil
					}
					return &pipeline.StepOutcome{RestartFrom: types.StepReview}, nil
				}
				return &pipeline.StepOutcome{}, nil
			}})
			appPaths := paths.WithRoot(t.TempDir())
			if err := appPaths.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			exec := pipeline.NewExecutor(sctx.DB, appPaths, sctx.Config, sctx.Agent, steps, nil)
			exec.SetForgeContext(&forgecontext.Context{Provider: provider, Host: "github.com"})
			var err error
			if strings.HasPrefix(scenario, "Test ") {
				done := make(chan error, 1)
				go func() { done <- exec.Execute(context.Background(), sctx.Run, sctx.Repo, dir) }()
				deadline := time.Now().Add(10 * time.Second)
				approved := false
				for time.Now().Before(deadline) {
					rows, readErr := sctx.DB.GetStepsByRun(sctx.Run.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					for _, row := range rows {
						if row.StepName == types.StepTest && row.Status == types.StepStatusAwaitingApproval {
							if _, respondErr := exec.RespondWithOverrides(types.StepTest, types.ActionApprove, nil, nil, nil, nil, "accept live validation exception"); respondErr == nil {
								approved = true
							}
						}
					}
					if approved {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !approved {
					t.Fatal("Test did not park for approval")
				}
				select {
				case err = <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("approved run did not complete")
				}
			} else {
				err = exec.Execute(context.Background(), sctx.Run, sctx.Repo, dir)
			}
			wantError := scenario == "fail after push" || scenario == "cancel after push"
			if (err != nil) != wantError {
				t.Fatalf("Execute = %v, wantError %v", err, wantError)
			}
			if scenario == "formatter head" {
				repaired = gitCmd(t, dir, "rev-parse", "HEAD")
			}
			if scenario == "recovered failure" {
				// A fresh executor has no in-memory reporter. Change its mutable head to
				// prove the failure update still uses the persisted publication SHA.
				sctx.Run.HeadSHA = base
				recovered := pipeline.NewExecutor(sctx.DB, appPaths, sctx.Config, sctx.Agent, []pipeline.Step{&PushStep{}, &transitionStep{name: types.StepCI, execute: func(*pipeline.StepContext) (*pipeline.StepOutcome, error) {
					return nil, errors.New("recovered monitor failed")
				}}}, nil)
				recovered.SetSkippedSteps([]types.StepName{types.StepPush})
				recovered.SetForgeContext(&forgecontext.Context{Provider: provider, Host: "github.com"})
				if err := recovered.Execute(context.Background(), sctx.Run, sctx.Repo, dir); err == nil {
					t.Fatal("recovered failure did not fail")
				}
			}
			mu.Lock()
			got := append([]statusRequest(nil), requests...)
			mu.Unlock()
			var want []statusRequest
			if scenario != "no push" && scenario != "forgejo" && scenario != "bitbucket" && scenario != "already upstream" {
				want = []statusRequest{{Path: "/repos/test/repo/statuses/" + head, State: "pending"}, {Path: "/repos/test/repo/statuses/" + head, State: "success"}}
				if wantError || scenario == "recovered failure" {
					want = append(want, statusRequest{Path: want[0].Path, State: "failure"})
				}
				if scenario == "repair head" || scenario == "CI repair readiness" {
					want = append(want, statusRequest{Path: "/repos/test/repo/statuses/" + repaired, State: "pending"}, statusRequest{Path: "/repos/test/repo/statuses/" + repaired, State: "success"})
				}
			}
			if uncertified {
				want = []statusRequest{{Path: "/repos/test/repo/statuses/" + head, State: "pending"}, {Path: "/repos/test/repo/statuses/" + head, State: "failure"}}
			}
			if scenario == "formatter head" {
				want = []statusRequest{{Path: "/repos/test/repo/statuses/" + repaired, State: "pending"}, {Path: "/repos/test/repo/statuses/" + repaired, State: "success"}}
			}
			if scenario == "fork" {
				for i := range want {
					want[i].Path = "/repos/fork/repo/statuses/" + head
				}
			}
			// Posting failures may be retried at terminal completion. The run must
			// nevertheless complete and expose the failure in its persisted logs.
			if scenario == "posting fails" {
				if len(got) < 2 {
					t.Fatalf("requests = %#v, want attempted pending and success", got)
				}
				logs, readErr := os.ReadFile(filepath.Join(appPaths.RunLogDir(sctx.Run.ID), "push.log"))
				if readErr != nil || !strings.Contains(string(logs), "commit status") || !strings.Contains(string(logs), "warning") {
					t.Fatalf("posting failure log = %s (%v)", logs, readErr)
				}
				return
			}
			if len(got) != len(want) {
				logs, _ := os.ReadFile(filepath.Join(appPaths.RunLogDir(sctx.Run.ID), "push.log"))
				gh, _ := os.ReadFile(ghLog)
				t.Fatalf("requests = %#v, want %#v; push logs %s; gh logs %s", got, want, logs, gh)
			}
			for i := range want {
				if got[i].Path != want[i].Path || got[i].State != want[i].State || got[i].Context != "no-mistakes" || got[i].Description == "" || got[i].TargetURL != "https://github.com/"+strings.Replace(strings.TrimPrefix(got[i].Path, "/repos/"), "/statuses/", "/commit/", 1) {
					t.Errorf("request %d = %#v, want %#v with context no-mistakes and description", i, got[i], want[i])
				}
			}
		})
	}
}
