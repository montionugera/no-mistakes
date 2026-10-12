package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// kingServerRunner is the only runner this fork uses. Nothing may run on a
// GitHub-hosted runner, so every job in every workflow must route here.
var kingServerRunner = []string{"self-hosted", "Linux", "X64", "king-server", "no-mistakes"}

const forkGuard = "github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository"

// TestEveryWorkflowJobRunsOnTheKingServer walks every workflow. Jobs of any
// pull_request workflow must also refuse external fork code before they are
// admitted onto the self-hosted runner.
func TestEveryWorkflowJobRunsOnTheKingServer(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(".github", "workflows", "*.y*ml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			On   any               `yaml:"on"`
			Jobs map[string]*wfJob `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if len(wf.Jobs) == 0 {
			t.Errorf("%s defines no jobs", path)
		}
		pullRequest := false
		switch on := wf.On.(type) {
		case map[string]any:
			_, pullRequest = on["pull_request"]
		case []any:
			pullRequest = slices.Contains(on, any("pull_request"))
		}
		for name, job := range wf.Jobs {
			if !slices.Equal(wfList(job.RunsOn), kingServerRunner) {
				t.Errorf("%s job %s runs-on = %v, want only %v", path, name, job.RunsOn, kingServerRunner)
			}
			if job.TimeoutMinutes < 1 {
				t.Errorf("%s job %s has no timeout-minutes", path, name)
			}
			if pullRequest && !strings.Contains(job.If, forkGuard) {
				t.Errorf("%s job %s must refuse external fork PR code before runner admission; if: %q", path, name, job.If)
			}
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["persist-credentials"] != "false" {
					t.Errorf("%s job %s checkout must set persist-credentials: false", path, name)
				}
			}
		}
	}
}
