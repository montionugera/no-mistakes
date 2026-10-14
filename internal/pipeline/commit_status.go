package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/forgecontext"
	"github.com/kunchenguid/no-mistakes/internal/safeurl"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const commitStatusTimeout = 15 * time.Second

// PublishedHeadStatusReporter is an optional step hook for best-effort status
// updates at run completion, failure, and CI readiness, including recovery.
type PublishedHeadStatusReporter interface {
	ReportPublishedHeadStatus(*StepContext, string, string)
}

// SetCommitStatusReporter binds a reporter to the exact head just pushed. A
// later push replaces it; it never derives a SHA from mutable worktree HEAD.
func (s *RunShared) SetCommitStatusReporter(report func(context.Context, string, string, func(string))) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitStatusReporter = report
}

func (s *RunShared) ReportCommitStatus(ctx context.Context, state, validatedSHA string, log func(string)) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	report := s.commitStatusReporter
	s.mu.Unlock()
	if report == nil {
		return false
	}
	report(ctx, state, validatedSHA, log)
	return true
}

func (e *Executor) reportPublishedHeadStatus(runCtx context.Context, run *db.Run, repo *db.Repo, state string, validatedSHAs ...string) {
	ctx, cancel := context.WithTimeout(runCtx, commitStatusTimeout)
	defer cancel()
	log := func(message string) {
		// Terminal updates use a fresh file handle: the Push step's log is closed.
		if e.paths != nil {
			if f, err := os.OpenFile(filepath.Join(e.paths.RunLogDir(run.ID), "push.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
				fmt.Fprintln(f, message)
				f.Close()
			}
		}
		e.emitLogChunk(run, repo, types.StepPush, message+"\n")
	}
	forge := e.forge
	if forge == nil {
		var profiles config.ForgeProfiles
		var plugins config.ProviderPlugins
		var err error
		if e.config != nil {
			profiles, plugins = e.config.ForgeProfiles, e.config.ProviderPlugins
		} else if e.paths != nil {
			var global *config.GlobalConfig
			global, err = config.LoadGlobal(e.paths.ConfigFile())
			if err == nil {
				profiles, plugins = global.ForgeProfiles, global.ProviderPlugins
			}
		}
		if err == nil {
			forge, err = forgecontext.Resolve(ctx, profiles, repo.UpstreamURL, repo.ForkURL)
		}
		if err == nil {
			err = forgecontext.RefuseProviderPluginOverlap(ctx, forge, plugins, repo.UpstreamURL)
		}
		if err != nil {
			message := safeurl.RedactText(fmt.Sprintf("warning: no-mistakes commit status resolve forge profile: %v", err))
			slog.Warn(message)
			log(message)
			return
		}
	}
	sctx := &StepContext{Ctx: ctx, Run: run, Repo: repo, DB: e.db, Config: e.config, ForgeContext: forge, WorkDir: e.workDir, Shared: e.shared, Log: log}
	for _, step := range e.steps {
		if reporter, ok := step.(PublishedHeadStatusReporter); ok {
			validatedSHA := ""
			if len(validatedSHAs) > 0 {
				validatedSHA = validatedSHAs[0]
			}
			reporter.ReportPublishedHeadStatus(sctx, state, validatedSHA)
		}
	}
}

func (e *Executor) ReportPublishedHeadFailure(ctx context.Context, run *db.Run, repo *db.Repo) {
	e.reportPublishedHeadStatus(ctx, run, repo, "failure")
}
