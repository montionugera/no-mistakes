package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/pipeline/steps"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"github.com/kunchenguid/no-mistakes/internal/worktrees"
)

func TestRecoverOnStartup_ReplacesPublishedSuccessWithFailure(t *testing.T) {
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
	(&steps.PushStep{}).ReportPublishedHeadStatus(&pipeline.StepContext{Ctx: context.Background(), DB: d, Run: run}, "success", "")
	publication, err := d.GetCommitStatusPublication(run.ID)
	if err != nil || publication.LastState != "success" {
		t.Fatalf("initial publication = %+v, %v", publication, err)
	}
	mgr := NewRunManager(d, p, nil)
	recoverOnStartup(d, p, mgr, worktrees.New(p, nil))
	recovered, err := d.GetRun(run.ID)
	if err != nil || recovered.Status != types.RunFailed {
		t.Fatalf("recovered run = %+v, %v", recovered, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) != 2 || states[0] != "success" || states[1] != "failure" {
		t.Fatalf("states = %v, want success then failure", states)
	}
}
