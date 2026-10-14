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

// beginPublishedCommitStatus binds status updates to the immutable SHA sent to
// git push, and to its target repository (the fork, when configured).
func beginPublishedCommitStatus(sctx *pipeline.StepContext, sha, target string, validated, ciEligible bool) func(string) {
	if resolvedProvider(sctx) != scm.ProviderGitHub || scm.ExtractHost(target) == "" {
		return func(string) {}
	}
	hostName := resolvedHost(sctx, target)
	publication := &db.CommitStatusPublication{SHA: sha, Host: hostName, Repo: github.RepoSlug(target), Validated: validated, CIEligible: ciEligible}
	if publication.Repo == "" {
		return func(string) {}
	}
	if err := sctx.DB.PutCommitStatusPublication(sctx.Run.ID, *publication); err != nil {
		statusWarning(sctx.Log, "record pushed head", err)
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
		mu.Lock()
		defer mu.Unlock()
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
		bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
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
	if sctx.Shared.ReportCommitStatus(sctx.Ctx, state, validatedSHA, sctx.Log) {
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
	report(sctx.Ctx, state, validatedSHA, sctx.Log)
}

// A skipped or overridden local gate cannot claim that all local validation
// passed. CI repairs published without revalidation therefore stay pending
// until exact-head CI readiness proves completion.
func localValidationPassed(sctx *pipeline.StepContext) bool {
	steps, err := sctx.DB.GetStepsByRun(sctx.Run.ID)
	if err != nil {
		return false
	}
	for _, name := range []types.StepName{types.StepReview, types.StepTest, types.StepDocument, types.StepLint} {
		passed := false
		for _, step := range steps {
			if step.StepName == name && step.Status == types.StepStatusCompleted && step.OverrideReason == nil && step.TestOverrideReason() == "" {
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
