package cli

import (
	"strings"
	"testing"
)

func TestDaemonNotifyPushRejectsInvalidVerificationPlanBeforeDial(t *testing.T) {
	cmd := newDaemonNotifyPushCmd()
	cmd.SetArgs([]string{
		"--gate", t.TempDir(),
		"--ref", "refs/heads/feature",
		"--old", "0000000000000000000000000000000000000000",
		"--new", "1111111111111111111111111111111111111111",
		"--push-option", verificationPlanPushOptionPrefix + "invalid",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid verification plan capture ID") {
		t.Fatalf("notify-push error = %v, want invalid verification plan capture ID", err)
	}
}
