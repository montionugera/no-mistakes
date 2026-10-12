package shipping

import (
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/scm/github"
)

// StatusIdentity is cached observability, never authority to export a receipt.
// Enrollment coordinates exist before Review; comparison authority does not.
type StatusIdentity struct {
	SourceRepo   string
	SourceBranch string
	TargetBranch string
	TargetSHA    string
	Generation   int64
}

// CachedStatusIdentity reads enrolled identity and any durable comparison only.
// It deliberately performs no Git, config, or forge reads.
func CachedStatusIdentity(database *db.DB, run *db.Run) *StatusIdentity {
	if run == nil || run.Shipping == nil {
		return nil
	}
	identity := &StatusIdentity{SourceBranch: strings.TrimPrefix(run.Branch, "refs/heads/"), TargetBranch: run.Shipping.TargetBranch}
	if repo, err := database.GetRepo(run.RepoID); err == nil {
		identity.SourceRepo = github.RepoSlug(repo.PushURL())
	}
	if comparison, err := database.GetReviewComparison(run.ID); err == nil && comparison != nil {
		identity.SourceRepo = comparison.SourceRepo
		identity.SourceBranch = comparison.SourceBranch
		identity.TargetBranch = comparison.TargetBranch
		identity.TargetSHA = comparison.TargetSHA
		identity.Generation = comparison.Generation
	}
	return identity
}
