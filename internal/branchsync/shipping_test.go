package branchsync

import (
	"github.com/kunchenguid/no-mistakes/internal/types"
	"testing"
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
