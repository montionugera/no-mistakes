package branchsync

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestImmutableSourceCustodyIsInspectionOnly(t *testing.T) {
	f := newSyncFixture(t)
	run, err := f.db.InsertRunWithShipping(f.repo.ID, "feature/sync", f.old, f.base, nil, "", "", "", "main", false, nil, &types.ShippingOptions{Mode: types.ShippingImmutableSource})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateRunStatus(run.ID, types.RunCompleted); err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateRunPRState(run.ID, "merged"); err != nil {
		t.Fatal(err)
	}
	state := f.service.InspectCached(f.ctx)
	if state.NextAction != nil {
		t.Fatalf("immutable promotion offers mutation: %+v", state.NextAction)
	}
	for _, action := range []func() State{func() State { return f.service.Apply(f.ctx) }, func() State { return f.service.Recover(f.ctx, false) }, func() State { return f.service.AdoptPublished(f.ctx) }} {
		got := action()
		if got.Changed || got.Safety != "immutable_source_inspection_only" {
			t.Fatalf("direct protected custody action was not refused: %+v", got)
		}
	}
	if got := mustRun(t, f.local, "rev-parse", "HEAD"); got != f.old {
		t.Fatalf("source checkout changed to %s", got)
	}
}

func TestImmutableSourceRefreshRefusesMovedTarget(t *testing.T) {
	f := newSyncFixture(t)
	mustRun(t, f.local, "push", "--force", f.remote, f.old+":refs/heads/feature/sync")
	mustRun(t, f.local, "push", "--force", f.remote, f.base+":refs/heads/main")
	_, err := f.db.InsertRunWithShipping(f.repo.ID, "feature/sync", f.old, f.base, nil, "", "", "", "main", false, nil, &types.ShippingOptions{
		Mode: types.ShippingImmutableSource, TargetBranch: "main", TargetSHA: f.base,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, f.local, "checkout", "main")
	mustWrite(t, filepath.Join(f.local, "target.txt"), "advanced\n")
	mustRun(t, f.local, "add", "target.txt")
	mustRun(t, f.local, "commit", "-m", "advance target")
	mustRun(t, f.local, "push", "--force", f.remote, "main:refs/heads/main")
	mustRun(t, f.local, "checkout", "feature/sync")
	state := f.service.Refresh(f.ctx)
	if state.State != StateTargetChanged || state.Safety != "immutable_source_moved" {
		t.Fatalf("moved target refresh state = %+v", state)
	}
}

func TestImmutableSourceRefreshBoundsSourceAndTargetReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "source", ref: "refs/heads/feature/sync"},
		{name: "target", ref: "refs/heads/main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			if _, err := f.db.InsertRunWithShipping(f.repo.ID, "feature/sync", f.pushed, f.base, nil, "", "", "", "main", false, nil, &types.ShippingOptions{
				Mode: types.ShippingImmutableSource, TargetBranch: "main", TargetSHA: f.base,
			}); err != nil {
				t.Fatal(err)
			}
			f.service.RemoteTimeout = 40 * time.Millisecond
			var deadline time.Time
			f.service.lsRemote = func(ctx context.Context, dir, remote, ref string) (string, error) {
				if ref == tc.ref {
					deadline, _ = ctx.Deadline()
					<-ctx.Done()
					return "", ctx.Err()
				}
				return f.pushed, nil
			}
			state := f.service.Refresh(f.ctx)
			if state.State != StateTargetChanged || deadline.IsZero() {
				t.Fatalf("bounded immutable refresh = %+v, deadline=%v", state, deadline)
			}
		})
	}
}
