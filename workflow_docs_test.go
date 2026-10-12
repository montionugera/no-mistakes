package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDocsWorkflowUsesScopedConcurrencyGroup(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/docs.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "group: pages-${{ github.event_name }}-${{ github.ref }}") {
		t.Fatalf("docs workflow must scope concurrency by event and ref")
	}
	if strings.Contains(content, "group: pages\n") {
		t.Fatalf("docs workflow must not use a global concurrency group")
	}
}

func TestDocsWorkflowSkipsPagesSetupOnPullRequests(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/docs.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "if: github.event_name != 'pull_request'") {
		t.Fatalf("docs workflow must skip Pages-only setup on pull requests")
	}
}

func TestDocsWorkflowJobsRunSelfHostedForSameRepositoryCodeOnly(t *testing.T) {
	raw, err := os.ReadFile(".github/workflows/docs.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf wfDoc
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"build", "deploy"} {
		job := wf.Jobs[name]
		if job == nil {
			t.Fatalf("docs workflow has no %s job", name)
		}
		if !slices.Equal(wfList(job.RunsOn), kingServerRunner) {
			t.Errorf("%s runs-on = %v", name, job.RunsOn)
		}
		if err := workflowAdmissionError(job.If); err != nil {
			t.Errorf("%s admission: %v", name, err)
		}
	}
	for _, name := range []string{"build", "deploy"} {
		for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
			got, err := evaluateWorkflowCondition(wf.Jobs[name].If, workflowConditionContext{
				EventName: event, Repository: "owner/repo", HeadRepo: "owner/repo", Ref: "refs/heads/feature", Author: "human-contributor",
			})
			if err != nil || got != (name == "build") {
				t.Errorf("%s on non-main %s runs = %t, error = %v", name, event, got, err)
			}
		}
	}
}
