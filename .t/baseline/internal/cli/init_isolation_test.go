package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/skill"
)

// Enrollment may share user HOME with another retained installation. Opting
// out must preserve its exact skill bytes; the ordinary default still installs.
func TestInitUserSkillInstallationChoice(t *testing.T) {
	for _, install := range []bool{false, true} {
		name := "opt-out"
		if install {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			setupTestRepo(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			for _, base := range skill.InstallBases {
				dest := filepath.Join(home, base, "no-mistakes", "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, []byte("retained-user-skill\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			p, err := paths.New()
			if err != nil {
				t.Fatal(err)
			}
			if err := p.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(p.DB())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			startTestDaemon(t, p, d)
			args := []string{"init"}
			if !install {
				args = append(args, "--install-skill=false")
			}
			output, err := executeCmd(args...)
			if err != nil {
				t.Fatalf("init enrollment: %v %s", err, output)
			}
			for _, base := range skill.InstallBases {
				content, err := os.ReadFile(filepath.Join(home, base, "no-mistakes", "SKILL.md"))
				if err != nil {
					t.Fatal(err)
				}
				expected := "retained-user-skill\n"
				if install {
					expected = skill.Markdown()
				}
				if string(content) != expected {
					t.Fatalf("user skill at %s changed unexpectedly", base)
				}
			}
			if !install && !strings.Contains(output, "disabled (--install-skill=false)") {
				t.Fatalf("opt-out not reported: %s", output)
			}
		})
	}
}
