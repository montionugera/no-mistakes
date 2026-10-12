package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These execute the managed shell boundary, rather than matching source text.
func TestReceiveHooksPinEnrollmentHomeAndExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell receive hooks")
	}
	for _, kind := range []string{"pre", "post"} {
		for _, missing := range []bool{false, true} {
			name := kind + "-present"
			if missing {
				name = kind + "-missing"
			}
			t.Run(name, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "enrolled home 'quoted'")
				bare := filepath.Join(root, "repos", "repo.git")
				if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0755); err != nil {
					t.Fatal(err)
				}
				root, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				bare = filepath.Join(root, "repos", "repo.git")
				pinned := filepath.Join(root, "pinned native")
				observed := filepath.Join(root, "observed-home")
				marker := filepath.Join(root, "wrong-executable")
				if !missing {
					source := "#!/bin/sh\nprintf '%s' \"$NM_HOME\" > " + shellSingleQuote(observed) + "\n"
					if err := os.WriteFile(pinned, []byte(source), 0755); err != nil {
						t.Fatal(err)
					}
				}
				poison := t.TempDir()
				if err := os.WriteFile(filepath.Join(poison, "no-mistakes"), []byte("#!/bin/sh\n: > "+shellSingleQuote(marker)+"\nexit 0\n"), 0755); err != nil {
					t.Fatal(err)
				}
				// Generate from a symlinked gate spelling; enrollment must resolve
				// its physical owner before the caller poisons the environment.
				t.Setenv("NM_HOME", root)
				alias := filepath.Join(t.TempDir(), "enrolled-gate-link")
				if err := os.Symlink(bare, alias); err != nil {
					t.Fatal(err)
				}
				_, nativeRoot, err := receiveHookOwner(alias)
				if err != nil {
					t.Fatal(err)
				}
				source := preReceiveHookScript(pinned, nativeRoot)
				if kind == "post" {
					source = postReceiveHookScript(pinned, nativeRoot)
				}
				hook := filepath.Join(bare, "hooks", kind+"-receive")
				if err := os.WriteFile(hook, []byte(source), 0755); err != nil {
					t.Fatal(err)
				}
				command := exec.Command("/bin/sh", hook)
				command.Dir = bare
				command.Env = append(os.Environ(), "NM_HOME="+poison, "GIT_DIR="+bare, "PATH="+poison+string(os.PathListSeparator)+os.Getenv("PATH"))
				command.Stdin = strings.NewReader("old new refs/heads/feature\n")
				output, runErr := command.CombinedOutput()
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("PATH substitute executed: %v %s", err, output)
				}
				if missing {
					if kind == "pre" && runErr == nil {
						t.Fatalf("missing pinned executable admitted push: %s", output)
					}
					if kind == "post" {
						if runErr != nil {
							t.Fatalf("post hook changed Git accepted outcome: %v", runErr)
						}
						logged, err := os.ReadFile(filepath.Join(bare, "notify-push.log"))
						if err != nil || !strings.Contains(string(logged), "notify-push failed") {
							t.Fatalf("notification failure missing: %v %s", err, logged)
						}
						if strings.Contains(string(output), "Pipeline started") {
							t.Fatal("missing helper claimed pipeline start")
						}
					}
				} else {
					if runErr != nil {
						t.Fatalf("pinned helper: %v %s", runErr, output)
					}
					actual, err := os.ReadFile(observed)
					if err != nil || string(actual) != root {
						t.Fatalf("helper home = %q, want enrolled %q (error %v)", actual, root, err)
					}
				}
			})
		}
	}
}
