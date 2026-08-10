package dependents

import (
	"testing"
	"time"
)

func TestFilterUsesConsumerPolicy(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{Repository: "https://example.com/active", RepositoryMetadata: RepositoryMetadata{PushedAt: now.Add(-24 * time.Hour)}},
		{Repository: "https://example.com/fork", RepositoryMetadata: RepositoryMetadata{Fork: true}},
		{Repository: "https://example.com/archive", RepositoryMetadata: RepositoryMetadata{Archived: true}},
		{Repository: "https://example.com/mirror", RepositoryMetadata: RepositoryMetadata{SourceName: "source/repo"}},
		{Repository: "https://example.com/stale", RepositoryMetadata: RepositoryMetadata{PushedAt: now.Add(-3 * 365 * 24 * time.Hour)}},
		{Repository: "https://example.com/unknown"},
	}

	kept, rejected := Filter(candidates, FilterOptions{
		ExcludeForks: true, ExcludeArchived: true, ExcludeMirrors: true,
		MaxAge: 2 * 365 * 24 * time.Hour, Now: now,
	})
	if len(kept) != 2 || kept[0].Repository != "https://example.com/active" || kept[1].Repository != "https://example.com/unknown" {
		t.Fatalf("kept = %+v", kept)
	}
	wantReasons := []string{ReasonFork, ReasonArchived, ReasonMirror, ReasonStale}
	if len(rejected) != len(wantReasons) {
		t.Fatalf("rejected = %+v", rejected)
	}
	for i, want := range wantReasons {
		if rejected[i].Reason != want {
			t.Errorf("rejected[%d].Reason = %q, want %q", i, rejected[i].Reason, want)
		}
	}
}

func TestFilterZeroOptionsKeepsAllCandidates(t *testing.T) {
	candidates := []Candidate{{Repository: "https://example.com/fork", RepositoryMetadata: RepositoryMetadata{Fork: true}}}
	kept, rejected := Filter(candidates, FilterOptions{})
	if len(kept) != 1 || len(rejected) != 0 {
		t.Fatalf("kept = %+v, rejected = %+v", kept, rejected)
	}
}

func TestFilterUsesConsumerAnalysisRequirements(t *testing.T) {
	candidates := []Candidate{
		{Repository: "https://example.com/not-analyzed"},
		{Repository: "https://example.com/no-tests", Analyzed: true, Analysis: Analysis{ImportFiles: 2}},
		{Repository: "https://example.com/no-imports", Analyzed: true, Analysis: Analysis{TestFiles: 2}},
		{Repository: "https://example.com/useful", Analyzed: true, Analysis: Analysis{TestFiles: 2, ImportFiles: 2}},
	}

	kept, rejected := Filter(candidates, FilterOptions{RequireTests: true, RequireImports: true})
	if len(kept) != 1 || kept[0].Repository != "https://example.com/useful" {
		t.Fatalf("kept = %+v", kept)
	}
	wantReasons := []string{ReasonNotAnalyzed, ReasonNoTests, ReasonNoImports}
	if len(rejected) != len(wantReasons) {
		t.Fatalf("rejected = %+v", rejected)
	}
	for i, want := range wantReasons {
		if rejected[i].Reason != want {
			t.Errorf("rejected[%d].Reason = %q, want %q", i, rejected[i].Reason, want)
		}
	}
}
