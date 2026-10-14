package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

const factsHead = "0123456789abcdef0123456789abcdef01234567"

func TestReadPRFacts(t *testing.T) {
	h, tr := newTestHost(t, Options{})
	tr.reply(`{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main","head_sha":"` + factsHead + `"},"state":"open"}`)
	facts, err := h.ReadPRFacts(context.Background(), testPR())
	if err != nil {
		t.Fatal(err)
	}
	if facts.SourceRepository != "team/repo" || facts.SourceBranch != "topic" || facts.HeadSHA != factsHead || facts.BaseBranch != "main" || facts.State != scm.PRStateOpen {
		t.Fatalf("facts = %+v", facts)
	}
	if len(tr.calls) != 1 || !strings.Contains(strings.Join(tr.last().argv, " "), "--repo=team/repo") {
		t.Fatalf("expected one scoped view, calls = %+v", tr.calls)
	}
}

func TestReadPRFactsRejectsIncompleteIdentity(t *testing.T) {
	for name, doc := range map[string]string{
		"missing head":   `{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main"},"state":"open"}`,
		"malformed head": `{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main","head_sha":"abc"},"state":"open"}`,
		"wrong number":   `{"pr":{"number":8,"url":"https://git.example.com/team/repo/pulls/8","head_branch":"topic","base_branch":"main","head_sha":"` + factsHead + `"},"state":"open"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, tr := newTestHost(t, Options{})
			tr.reply(doc)
			_, err := h.ReadPRFacts(context.Background(), testPR())
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadPRFactsRequiresMergeProof(t *testing.T) {
	h, tr := availableHost(t, statusAllCaps, Options{})
	tr.reply(`{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main","head_sha":"`+factsHead+`"},"state":"merged"}`, `{"merged":true,"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_sha":"`+factsHead+`","merge_commit_sha":"bad","merged_at":"2026-01-01T00:00:00Z"}`)
	_, err := h.ReadPRFacts(context.Background(), testPR())
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v", err)
	}
}

func TestFindOpenPRFacts(t *testing.T) {
	h, tr := newTestHost(t, Options{})
	tr.reply(`{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main","head_sha":"`+factsHead+`"}}`, `{"pr":{"number":7,"url":"https://git.example.com/team/repo/pulls/7","head_branch":"topic","base_branch":"main","head_sha":"`+factsHead+`"},"state":"open"}`)
	facts, err := h.FindOpenPRFacts(context.Background(), "team/repo", "topic")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].SourceBranch != "topic" {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestFindOpenPRFactsRejectsWrongSource(t *testing.T) {
	h, tr := newTestHost(t, Options{})
	if _, err := h.FindOpenPRFacts(context.Background(), "someone/else", "topic"); err == nil {
		t.Fatal("wrong source repository was accepted")
	}
	if len(tr.calls) != 0 {
		t.Fatalf("wrong source caused plugin calls: %+v", tr.calls)
	}
}

func TestGetChecksPreservesReportedHeadSHA(t *testing.T) {
	h, tr := newTestHost(t, Options{})
	requested := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tr.reply(`{"checks":[{"name":"build","bucket":"pass","head_sha":"` + factsHead + `"}]}`)
	checks, err := h.GetChecks(context.Background(), &scm.PR{Number: "7", URL: testPR().URL, HeadSHA: requested})
	if err != nil {
		t.Fatal(err)
	}
	if checks[0].HeadSHA != factsHead {
		t.Fatalf("check head = %q, want reported %q", checks[0].HeadSHA, factsHead)
	}
}
