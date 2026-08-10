package dependents

import "sort"

const (
	defaultRepositoryWeight = 10
	defaultImportWeight     = 1_000_000
	defaultTestWeight       = 200_000
	defaultPopularityDamp   = 100
)

// ScoreFunc assigns a ranking score to a candidate.
type ScoreFunc func(Candidate) int64

// PopularityScore returns registry downloads when available and otherwise
// uses dependent repository count.
func PopularityScore(candidate Candidate) int64 {
	if candidate.Downloads > 0 {
		return candidate.Downloads
	}
	return int64(candidate.DependentRepos)*defaultRepositoryWeight + int64(candidate.RepositoryMetadata.StargazersCount)
}

// DefaultScore favors repositories with source references and tests after
// analysis, with popularity as a tiebreaker. Before analysis it returns the
// popularity score.
func DefaultScore(candidate Candidate) int64 {
	popularity := PopularityScore(candidate)
	if !candidate.Analyzed {
		return popularity
	}
	return int64(candidate.Analysis.ImportFiles)*defaultImportWeight +
		int64(candidate.Analysis.TestFiles)*defaultTestWeight +
		popularity/defaultPopularityDamp
}

// Rank returns a ranked copy of candidates. A non-positive limit keeps all
// candidates. A nil scorer uses DefaultScore.
func Rank(candidates []Candidate, limit int, scorer ScoreFunc) []Candidate {
	if scorer == nil {
		scorer = DefaultScore
	}
	out := append([]Candidate(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := scorer(out[i]), scorer(out[j])
		if left != right {
			return left > right
		}
		if out[i].DependentRepos != out[j].DependentRepos {
			return out[i].DependentRepos > out[j].DependentRepos
		}
		if out[i].Downloads != out[j].Downloads {
			return out[i].Downloads > out[j].Downloads
		}
		return canonicalRepository(out[i].Repository) < canonicalRepository(out[j].Repository)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
