package github

import (
	"context"
	"fmt"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"strings"
	"testing"
)

func TestPRFactsBindsAPIHostAndImmutableCoordinates(t *testing.T) {
	url := "https://ghe.example.test/owner/repo/pull/42"
	body := fmt.Sprintf(`{"number":42,"html_url":%q,"state":"open","draft":true,"head":{"ref":"feature","sha":%q,"repo":{"full_name":"owner/repo"}},"base":{"ref":"main","sha":%q,"repo":{"full_name":"owner/repo"}}}`, url, strings.Repeat("a", 40), strings.Repeat("b", 40))
	host := New(githubTestCmdFactory(map[string]githubTestResponse{"gh api repos/owner/repo/pulls/42 --hostname ghe.example.test": {stdout: body}}), nil, "ghe.example.test", "owner/repo")
	got, err := host.GetPRFacts(context.Background(), &scm.PR{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != scm.PRStateOpen || !got.Draft || got.SourceBranch != "feature" || got.TargetBranch != "main" || got.HeadSHA != strings.Repeat("a", 40) {
		t.Fatalf("facts: %+v", got)
	}
	for _, bad := range []string{`{`, strings.Replace(body, `"number":42`, `"number":43`, 1), strings.Replace(body, url, "https://ghe.example.test/owner/other/pull/42", 1)} {
		host := New(githubTestCmdFactory(map[string]githubTestResponse{"gh api repos/owner/repo/pulls/42 --hostname ghe.example.test": {stdout: bad}}), nil, "ghe.example.test", "owner/repo")
		if _, err := host.GetPRFacts(context.Background(), &scm.PR{URL: url}); err == nil {
			t.Fatal("malformed or wrong PR identity accepted")
		}
	}
}
