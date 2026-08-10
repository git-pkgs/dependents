package dependents

import (
	"reflect"
	"testing"
)

func TestRankUsesAnalysisThenPopularity(t *testing.T) {
	candidates := []Candidate{
		{Repository: "https://example.com/popular", Downloads: 1_000_000},
		{
			Repository: "https://example.com/exercised", Downloads: 10,
			Analyzed: true, Analysis: Analysis{ImportFiles: 3, TestFiles: 2},
		},
		{
			Repository: "https://example.com/tested", Downloads: 5,
			Analyzed: true, Analysis: Analysis{ImportFiles: 3, TestFiles: 5},
		},
	}
	original := append([]Candidate(nil), candidates...)

	got := Rank(candidates, 2, nil)
	if len(got) != 2 || got[0].Repository != "https://example.com/tested" || got[1].Repository != "https://example.com/exercised" {
		t.Fatalf("ranked = %+v", got)
	}
	if !reflect.DeepEqual(candidates, original) {
		t.Error("Rank modified its input")
	}
}

func TestRankAcceptsConsumerScorer(t *testing.T) {
	candidates := []Candidate{
		{Repository: "https://example.com/a", Packages: []Package{{Name: "one"}}},
		{Repository: "https://example.com/b", Packages: []Package{{Name: "one"}, {Name: "two"}}},
	}
	got := Rank(candidates, 1, func(candidate Candidate) int64 { return int64(len(candidate.Packages)) })
	if len(got) != 1 || got[0].Repository != "https://example.com/b" {
		t.Fatalf("ranked = %+v", got)
	}
}

func TestPopularityScoreFallsBackToDependentRepositories(t *testing.T) {
	if got := PopularityScore(Candidate{DependentRepos: 12, RepositoryMetadata: RepositoryMetadata{StargazersCount: 5}}); got != 125 {
		t.Errorf("score = %d, want 125", got)
	}
}
