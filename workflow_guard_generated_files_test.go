package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGuardGeneratedFilesWorkflowCoversReleasePleaseArtifacts pins the list of
// guarded paths. If release-please starts managing more files, add them here
// and to the workflow together.
func TestGuardGeneratedFilesWorkflowCoversReleasePleaseArtifacts(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/guard-generated-files.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	content := string(data)

	guarded := []string{
		"CHANGELOG.md",
		".release-please-manifest.json",
	}
	for _, path := range guarded {
		if !strings.Contains(content, path) {
			t.Errorf("workflow must guard %q", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("guarded path %q not present in repo: %v", path, err)
		}
	}
}

// TestGuardGeneratedFilesWorkflowExemptsReleasePlease ensures the release
// pipeline's own PR (which legitimately modifies the generated files) is
// always allowed through.
func TestGuardGeneratedFilesWorkflowExemptsReleasePlease(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/guard-generated-files.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var wf wfDoc
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	job := wf.Jobs["check"]
	if job == nil {
		t.Fatal("missing check job")
	}
	if err := workflowAdmissionError(job.If); err != nil {
		t.Errorf("check admission: %v", err)
	}
	for _, login := range []string{"github-actions[bot]", "release-please[bot]", "human-contributor", "unlisted-automation[bot]"} {
		got, err := evaluateWorkflowCondition(job.If, workflowConditionContext{
			EventName: "pull_request", Repository: "owner/repo", HeadRepo: "owner/repo", Author: login,
		})
		want := login == "human-contributor" || login == "unlisted-automation[bot]"
		if err != nil || got != want {
			t.Errorf("author %s runs = %t, want %t, error = %v", login, got, want, err)
		}
	}
}

// TestGuardGeneratedFilesWorkflowUsesGitDiffWithFullHistory pins the
// git-based file-diff approach. Using the API would add a permission surface
// (pull-requests: read), rate-limit exposure, and pagination concerns; the
// git three-dot diff matches exactly what GitHub shows in "Files changed".
func TestGuardGeneratedFilesWorkflowUsesGitDiffWithFullHistory(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/guard-generated-files.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "actions/checkout") {
		t.Errorf("workflow must check out the repo to run git diff locally")
	}
	if !strings.Contains(content, "fetch-depth: 0") {
		t.Errorf("workflow must use fetch-depth: 0 so merge-base for base...head is available")
	}
	if !strings.Contains(content, `git diff --name-only "${BASE_SHA}...${HEAD_SHA}"`) {
		t.Errorf("workflow must use 'git diff --name-only base...head' (three-dot) for PR file list")
	}
	if strings.Contains(content, "gh api") {
		t.Errorf("workflow must not fall back to the GitHub API for file listing")
	}
	if strings.Contains(content, "pull-requests:") {
		t.Errorf("workflow must not request pull-requests permission once switched to git diff")
	}
}

// TestGuardGeneratedFilesWorkflowTriggersOnPushedCommits ensures the guard
// re-runs when new commits are pushed to a PR (the synchronize event), so a
// contributor cannot open a clean PR then push a commit that edits CHANGELOG.md.
func TestGuardGeneratedFilesWorkflowTriggersOnPushedCommits(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/guard-generated-files.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	content := string(data)

	for _, typ := range []string{"opened", "synchronize", "reopened"} {
		if !strings.Contains(content, typ) {
			t.Errorf("workflow must trigger on pull_request type %q", typ)
		}
	}
}
