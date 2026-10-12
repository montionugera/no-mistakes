package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

func (h *Host) GetPRFacts(ctx context.Context, pr *scm.PR) (scm.PRFacts, error) {
	n, err := parsePullRequestURL(pr.URL, h.host, RepoSlug(pr.URL))
	if err != nil {
		return scm.PRFacts{}, err
	}
	args := []string{"api", "repos/" + RepoSlug(pr.URL) + "/pulls/" + strconv.Itoa(n)}
	if h.host != "" {
		args = append(args, "--hostname", h.host)
	}
	out, err := h.cmd(ctx, "gh", args...).Output()
	if err != nil {
		return scm.PRFacts{}, fmt.Errorf("read exact GitHub PR facts: %w", err)
	}
	type ref struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	}
	var body struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
		Draft  bool   `json:"draft"`
		Head   ref    `json:"head"`
		Base   ref    `json:"base"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return scm.PRFacts{}, fmt.Errorf("invalid GitHub PR facts")
	}
	if body.Number != n || body.URL != pr.URL {
		return scm.PRFacts{}, fmt.Errorf("GitHub PR facts identity mismatch")
	}
	state := scm.PRStateClosed
	if body.Merged {
		state = scm.PRStateMerged
	} else if body.State == "open" {
		state = scm.PRStateOpen
	}
	return scm.PRFacts{URL: body.URL, State: state, Draft: body.Draft, SourceRepo: body.Head.Repo.FullName, SourceBranch: body.Head.Ref, HeadSHA: body.Head.SHA, TargetRepo: body.Base.Repo.FullName, TargetBranch: body.Base.Ref, TargetSHA: body.Base.SHA}, nil
}
