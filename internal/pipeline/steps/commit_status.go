package steps

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/safeurl"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/scm/github"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Plugins currently expose PR/CI operations, but no commit-status capability.
// Their host ownership must also apply to persisted publications from older runs.
func suppressPluginCommitStatus(sctx *pipeline.StepContext, hosts ...string) bool {
	name, owned := "", false
	if sctx.Repo != nil {
		name, owned = providerPluginForStep(sctx)
	}
	if !owned && sctx.Config != nil {
		for _, host := range hosts {
			if name, owned = sctx.Config.ProviderPlugins.Select(host, nil); owned {
				break
			}
		}
	}
	if owned {
		statusWarning(sctx.Log, "suppressed", fmt.Errorf("provider plugin %q does not support commit statuses", name))
	}
	return owned
}

func recordCommitStatusAttempt(sctx *pipeline.StepContext, sha, target, ref string, validated, ciEligible bool) error {
	if suppressPluginCommitStatus(sctx) || resolvedProvider(sctx) != scm.ProviderGitHub || scm.ExtractHost(target) == "" {
		return nil
	}
	p := db.CommitStatusAttempt{CommitStatusPublication: db.CommitStatusPublication{SHA: sha, Host: resolvedHost(sctx, target), Repo: github.RepoSlug(target), Validated: validated, CIEligible: ciEligible}, Target: safeurl.Redact(target), Ref: ref}
	if p.Repo == "" {
		return nil
	}
	return sctx.DB.PutCommitStatusAttempt(sctx.Run.ID, p)
}

// reconcileCommitStatusAttempt promotes only an attempted SHA observed at the
// recorded target ref. It never infers publication from the mutable run head.
func reconcileCommitStatusAttempt(sctx *pipeline.StepContext) (bool, error) {
	attempt, err := sctx.DB.GetCommitStatusAttempt(sctx.Run.ID)
	if err != nil || attempt == nil {
		return false, err
	}
	if newer, err := sctx.DB.HasNewerCommitStatusPublication(sctx.Ctx, sctx.Run.ID, attempt.CommitStatusPublication); err != nil || newer {
		return false, err
	}
	target := attempt.Target
	if sctx.Repo != nil {
		if current := resolvePushURL(sctx); safeurl.Redact(current) == target {
			target = current
		}
	}
	remote, err := lsRemoteSHA(func(args ...string) (string, error) { return stepGitRun(sctx, args...) }, target, attempt.Ref)
	if err != nil {
		return false, err
	}
	if remote != attempt.SHA {
		return false, nil
	}
	if err := sctx.DB.ConfirmCommitStatusAttempt(sctx.Run.ID, attempt.CommitStatusPublication); err != nil {
		return false, err
	}
	return true, nil
}

func beginPublishedCommitStatus(sctx *pipeline.StepContext) func(string) {
	if suppressPluginCommitStatus(sctx) {
		return func(string) {}
	}
	confirmed, err := reconcileCommitStatusAttempt(sctx)
	if err != nil {
		statusWarning(sctx.Log, "confirm pushed head", err)
	}
	if !confirmed {
		return func(string) {}
	}
	publication, err := sctx.DB.GetCommitStatusPublication(sctx.Run.ID)
	if err != nil {
		statusWarning(sctx.Log, "read pushed head", err)
	}
	if publication == nil {
		return func(string) {}
	}
	report := commitStatusReporter(sctx, publication)
	sctx.Shared.SetCommitStatusReporter(report)
	update := func(state string) { report(sctx.Ctx, state, "", sctx.Log) }
	update("pending")
	return update
}

func statusWarning(log func(string), operation string, err error) {
	message := safeurl.RedactText(fmt.Sprintf("warning: no-mistakes commit status %s: %v", operation, err))
	slog.Warn(message)
	if log != nil {
		log(message)
	}
}

func commitStatusReporter(sctx *pipeline.StepContext, publication *db.CommitStatusPublication) func(context.Context, string, string, func(string)) {
	host := github.New(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return stepCmdContext(sctx, ctx, name, args...)
	}, nil, publication.Host, publication.Repo)
	targetURL := "https://" + publication.Host + "/" + publication.Repo + "/commit/" + publication.SHA
	sha := publication.SHA
	var mu sync.Mutex
	lastState := publication.LastState
	report := func(ctx context.Context, state, validatedSHA string, log func(string)) {
		if suppressPluginCommitStatus(sctx, publication.Host) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		newer, err := sctx.DB.HasNewerCommitStatusPublication(ctx, sctx.Run.ID, *publication)
		if err != nil {
			statusWarning(log, "check publication ownership", err)
			return
		}
		if newer {
			return
		}
		if state == "success" && publication.CIEligible && validatedSHA == publication.SHA {
			publication.Validated = true
		}
		if state == "success" && !publication.Validated {
			if sctx.Run.Status != types.RunCompleted {
				return
			}
			state = "failure"
		}
		if state == lastState {
			return
		}
		description := "no-mistakes pipeline running"
		switch state {
		case "success":
			description = "no-mistakes validation passed"
		case "failure":
			description = "no-mistakes pipeline failed or cancelled"
		}
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := host.PostCommitStatus(bounded, sha, state, description, targetURL); err != nil {
			statusWarning(log, state+" for "+sha, err)
			return
		}
		lastState = state
		publication.LastState = state
		if err := sctx.DB.PutCommitStatusPublication(sctx.Run.ID, *publication); err != nil {
			statusWarning(log, "record posted state", err)
		}
	}
	return report
}

func (*PushStep) ReportPublishedHeadStatus(sctx *pipeline.StepContext, state, validatedSHA string) {
	if suppressPluginCommitStatus(sctx) {
		return
	}
	confirmed, err := reconcileCommitStatusAttempt(sctx)
	if err != nil {
		statusWarning(sctx.Log, "reconcile push attempt", err)
	}
	if !confirmed && sctx.Shared.ReportCommitStatus(sctx.Ctx, state, validatedSHA, sctx.Log) {
		return
	}
	publication, err := sctx.DB.GetCommitStatusPublication(sctx.Run.ID)
	if err != nil {
		statusWarning(sctx.Log, "read pushed head", err)
		return
	}
	if publication == nil {
		return
	}
	report := commitStatusReporter(sctx, publication)
	sctx.Shared.SetCommitStatusReporter(report)
	if confirmed {
		report(sctx.Ctx, "pending", "", sctx.Log)
	}
	report(sctx.Ctx, state, validatedSHA, sctx.Log)
}

// A skipped or overridden local gate cannot claim that all local validation
// passed. CI repairs published without revalidation therefore stay pending
// until exact-head CI readiness proves completion.
func localValidationPassed(sctx *pipeline.StepContext, head string) bool {
	if head != "" {
		run, err := sctx.DB.GetRun(sctx.Run.ID)
		if err != nil || run == nil || run.ReviewApprovedHeadSHA == nil || *run.ReviewApprovedHeadSHA != head {
			return false
		}
	}
	steps, err := sctx.DB.GetStepsByRun(sctx.Run.ID)
	if err != nil {
		return false
	}
	for _, name := range []types.StepName{types.StepReview, types.StepTest, types.StepDocument, types.StepLint} {
		passed := false
		for _, step := range steps {
			if step.StepName == name && step.Status == types.StepStatusCompleted && step.OverrideReason == nil && step.TestOverrideReason() == "" {
				if name == types.StepTest && head != "" {
					if step.FindingsJSON == nil {
						return false
					}
					findings, err := types.ParseFindingsJSON(*step.FindingsJSON)
					if err != nil || findings.TestedHeadSHA != head {
						return false
					}
				}
				passed = true
				break
			}
		}
		if !passed {
			return false
		}
	}
	return true
}
