package steps

import (
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"testing"
	"time"
)

func TestShippingPeriodicCIPollRetainsFiveMinuteMinimum(t *testing.T) {
	sctx := &pipeline.StepContext{Run: &db.Run{Shipping: &types.ShippingOptions{Mode: types.ShippingFull}}}
	for _, elapsed := range []time.Duration{0, time.Minute, 20 * time.Minute} {
		if got := ciPollInterval(sctx, elapsed, time.Second); got < 5*time.Minute {
			t.Fatalf("shipping poll %v", got)
		}
	}
	sctx.Run.Shipping = nil
	if got := ciPollInterval(sctx, 0, time.Second); got != time.Second {
		t.Fatalf("ordinary cadence changed: %v", got)
	}
}

func TestStrictShippingUsesPersistedBaseOverChangedConfiguration(t *testing.T) {
	ctx := &pipeline.StepContext{Run: &db.Run{Shipping: &types.ShippingOptions{Mode: types.ShippingFull, TargetBranch: "dev"}}, Repo: &db.Repo{DefaultBranch: "main"}, Config: &config.Config{}}
	ctx.Config.PR.BaseBranch = "other"
	if got := effectivePRBaseBranch(ctx); got != "dev" {
		t.Fatalf("strict pinned target replaced by config: %q", got)
	}
	ctx.Run.Shipping = nil
	if got := effectivePRBaseBranch(ctx); got != "other" {
		t.Fatalf("ordinary configured target changed: %q", got)
	}
}
