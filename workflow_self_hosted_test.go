package main

import (
	"fmt"
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
		for _, problem := range kingServerWorkflowProblems(raw) {
			t.Errorf("%s: %s", path, problem)
		}
	}
}

func kingServerWorkflowProblems(raw []byte) []string {
	var wf wfDoc
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return []string{fmt.Sprintf("parse workflow: %v", err)}
	}
	var problems []string
	if len(wf.Jobs) == 0 {
		problems = append(problems, "workflow defines no jobs")
	}
	events := wfList(wf.On)
	if on, ok := wf.On.(map[string]any); ok {
		for event := range on {
			events = append(events, event)
		}
	}
	for name, job := range wf.Jobs {
		if !slices.Equal(wfList(job.RunsOn), kingServerRunner) {
			problems = append(problems, fmt.Sprintf("job %s runs-on = %v, want only %v", name, job.RunsOn, kingServerRunner))
		}
		if job.TimeoutMinutes < 1 {
			problems = append(problems, fmt.Sprintf("job %s has no timeout-minutes", name))
		}
		if slices.Contains(events, "pull_request") {
			if err := workflowAdmissionError(job.If); err != nil {
				problems = append(problems, fmt.Sprintf("job %s admission: %v", name, err))
			}
		}
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["persist-credentials"] != "false" {
				problems = append(problems, fmt.Sprintf("job %s checkout must set persist-credentials: false", name))
			}
		}
	}
	return problems
}

func TestKingServerWorkflowRejectsUnsafeAdmission(t *testing.T) {
	for _, trigger := range []string{"pull_request", "[pull_request]", "{pull_request: {}}"} {
		for _, condition := range []string{
			"",
			"(github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository) || github.event_name == 'pull_request'",
		} {
			t.Run(trigger+"/"+condition, func(t *testing.T) {
				raw := fmt.Sprintf("on: %s\njobs:\n  check:\n    runs-on: [self-hosted, Linux, X64, king-server, no-mistakes]\n    timeout-minutes: 10\n", trigger)
				if condition != "" {
					raw += "    if: " + condition + "\n"
				}
				if problems := kingServerWorkflowProblems([]byte(raw)); len(problems) != 1 {
					t.Fatalf("unsafe admission problems = %v, want one rejection", problems)
				}
			})
		}
		t.Run(trigger+"/safe", func(t *testing.T) {
			raw := fmt.Sprintf("on: %s\njobs:\n  check:\n    runs-on: [self-hosted, Linux, X64, king-server, no-mistakes]\n    timeout-minutes: 10\n    if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository\n", trigger)
			if problems := kingServerWorkflowProblems([]byte(raw)); len(problems) != 0 {
				t.Fatalf("safe admission rejected: %v", problems)
			}
		})
	}
}
