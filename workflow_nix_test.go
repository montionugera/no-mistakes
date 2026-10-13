package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNixWorkflowBuildsAndChecksTheFlake(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(".github", "workflows", "nix.yml"))
	if err != nil {
		t.Fatalf("read Nix workflow: %v", err)
	}
	var wf wfDoc
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse Nix workflow: %v", err)
	}
	job, ok := wf.Jobs["nix"]
	if !ok {
		t.Fatal("Nix workflow has no nix job")
	}
	if !slices.Equal(wfList(job.RunsOn), kingServerRunner) {
		t.Errorf("nix job runs-on = %v, want the self-hosted king-server runner", job.RunsOn)
	}
	// The runner account has no sudo, so an installer step can never succeed;
	// the job must use the administrator-preinstalled multi-user Nix.
	if slices.ContainsFunc(job.Steps, func(step wfStep) bool {
		return strings.HasPrefix(step.Uses, "DeterminateSystems/nix-installer-action@")
	}) {
		t.Error("nix job must not run the Nix installer: the runner account has no sudo")
	}
	setupIndex := slices.IndexFunc(job.Steps, func(step wfStep) bool {
		return step.Name == "Use the runner's preinstalled Nix"
	})
	if setupIndex < 0 {
		t.Fatal("nix job must have the preinstalled Nix setup step")
	}
	t.Run("preinstalled Nix PATH setup", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the runner setup requires bash and Unix executable permissions")
		}
		for _, present := range []bool{true, false} {
			name := "missing"
			if present {
				name = "installed"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				nixBin := filepath.Join(dir, "nix profile", "bin")
				if present {
					if err := os.MkdirAll(nixBin, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(nixBin, "nix"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				pathFile := filepath.Join(dir, "github-path")
				if err := os.WriteFile(pathFile, nil, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("NIX_PROFILE_BIN", nixBin)
				t.Setenv("GITHUB_PATH", pathFile)
				cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", job.Steps[setupIndex].Run)
				cmd.Dir = dir
				output, runErr := cmd.CombinedOutput()
				path, err := os.ReadFile(pathFile)
				if err != nil {
					t.Fatal(err)
				}
				if present {
					if runErr != nil {
						t.Fatalf("installed Nix setup failed: %v\n%s", runErr, output)
					}
					if string(path) != nixBin+"\n" {
						t.Errorf("GITHUB_PATH = %q, want %q", path, nixBin+"\n")
					}
				} else {
					if exitErr, ok := runErr.(*exec.ExitError); !ok || exitErr.ExitCode() == 0 {
						t.Errorf("missing Nix must exit non-zero, got %v\n%s", runErr, output)
					}
					if !strings.Contains(string(output), "::error::") || !strings.Contains(string(output), "administrator must preinstall") {
						t.Errorf("missing Nix must explain administrator setup: %s", output)
					}
					if len(path) != 0 {
						t.Errorf("missing Nix wrote GITHUB_PATH: %q", path)
					}
				}
			})
		}
	})
	var commands []string
	for _, command := range workflowCommandsMatching(job.Steps, func(wfStep) bool { return true }) {
		commands = append(commands, strings.Join(append([]string{command.name}, command.args...), " "))
	}
	for _, want := range []string{
		"nix flake check --all-systems --no-build",
		"set -o pipefail",
		"./result/bin/no-mistakes --version",
		"nix-build default.nix",
	} {
		if !slices.ContainsFunc(commands, func(command string) bool { return strings.HasPrefix(command, want) }) {
			t.Errorf("nix job must run %q; normalized commands: %q", want, commands)
		}
	}
}

func TestNixWorkflowRunsOnEveryVendorHashInput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(".github", "workflows", "nix.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			PullRequest struct {
				Paths []string `yaml:"paths"`
			} `yaml:"pull_request"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	paths := wf.On.PullRequest.Paths
	for _, want := range []string{"go.mod", "go.sum", "package.nix", "flake.nix", "flake.lock", "default.nix"} {
		if !slices.Contains(paths, want) {
			t.Errorf("Nix workflow pull_request paths must include %q, got %v", want, paths)
		}
	}
}
