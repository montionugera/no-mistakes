package git

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestWithPreparedRefTransactionHoldsBothDirectRefs(t *testing.T) {
	t.Parallel()
	dir := initTestRepo(t)
	ctx := context.Background()
	head := run(t, dir, "git", "rev-parse", "HEAD")
	refs := []string{"refs/heads/recovery-test", "refs/no-mistakes/recover/test-run"}
	commands := ""
	for _, ref := range refs {
		run(t, dir, "git", "update-ref", ref, head)
		commands += fmt.Sprintf("verify %s %s\n", ref, head)
	}
	called := false
	err := WithPreparedRefTransaction(ctx, dir, commands, func() error {
		called = true
		for _, ref := range refs {
			if _, err := Run(ctx, dir, "-c", "core.filesRefLockTimeout=0", "symbolic-ref", ref, "refs/heads/other"); err == nil {
				return fmt.Errorf("writer replaced locked ref %s", ref)
			}
			target, exists, err := DirectRefTarget(ctx, dir, ref)
			if err != nil || !exists || target != head {
				return fmt.Errorf("locked ref %s changed: %s, %v", ref, target, err)
			}
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("prepared transaction = %v, callback ran = %v", err, called)
	}
	for _, ref := range refs {
		if _, err := Run(ctx, dir, "update-ref", "-d", ref); err != nil {
			t.Fatalf("lock survived transaction: %v", err)
		}
	}
}

func TestWithPreparedRefTransactionRejectsWithoutCreatingAnchor(t *testing.T) {
	t.Parallel()
	dir := initTestRepo(t)
	head := run(t, dir, "git", "rev-parse", "HEAD")
	ref := "refs/no-mistakes/recover/test-run"
	rejected := errors.New("recovery evidence changed")
	err := WithPreparedRefTransaction(context.Background(), dir, fmt.Sprintf("create %s %s\n", ref, head), func() error { return rejected })
	if !errors.Is(err, rejected) {
		t.Fatalf("transaction error = %v", err)
	}
	_, exists, err := DirectRefTarget(context.Background(), dir, ref)
	if err != nil || exists {
		t.Fatalf("rejected transaction created anchor: exists %v, err %v", exists, err)
	}
	run(t, dir, "git", "update-ref", ref, head)
}
