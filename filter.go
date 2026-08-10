package dependents

import "time"

const (
	ReasonFork        = "fork"
	ReasonArchived    = "archived"
	ReasonMirror      = "mirror"
	ReasonStale       = "stale"
	ReasonNotAnalyzed = "not analyzed"
	ReasonNoTests     = "no tests"
	ReasonNoImports   = "no upstream references"
)

// FilterOptions lets each consumer choose its repository eligibility policy.
// A zero MaxAge does not filter stale repositories. Missing push dates are
// retained.
type FilterOptions struct {
	ExcludeForks    bool
	ExcludeArchived bool
	ExcludeMirrors  bool
	MaxAge          time.Duration
	Now             time.Time
	RequireAnalyzed bool
	RequireTests    bool
	RequireImports  bool
}

// Rejection records a candidate excluded by Filter and the first matching
// reason.
type Rejection struct {
	Candidate Candidate
	Reason    string
}

// Filter applies repository health policy without changing candidates.
func Filter(candidates []Candidate, opts FilterOptions) ([]Candidate, []Rejection) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	kept := make([]Candidate, 0, len(candidates))
	rejected := make([]Rejection, 0)
	for _, candidate := range candidates {
		reason := rejectionReason(candidate, opts, now)
		if reason == "" {
			kept = append(kept, candidate)
			continue
		}
		rejected = append(rejected, Rejection{Candidate: candidate, Reason: reason})
	}
	return kept, rejected
}

func rejectionReason(candidate Candidate, opts FilterOptions, now time.Time) string {
	metadata := candidate.RepositoryMetadata
	switch {
	case opts.ExcludeForks && metadata.Fork:
		return ReasonFork
	case opts.ExcludeArchived && metadata.Archived:
		return ReasonArchived
	case opts.ExcludeMirrors && (metadata.MirrorURL != "" || metadata.SourceName != ""):
		return ReasonMirror
	case opts.MaxAge > 0 && !metadata.PushedAt.IsZero() && now.Sub(metadata.PushedAt) > opts.MaxAge:
		return ReasonStale
	case (opts.RequireAnalyzed || opts.RequireTests || opts.RequireImports) && !candidate.Analyzed:
		return ReasonNotAnalyzed
	case opts.RequireTests && candidate.Analysis.TestFiles == 0:
		return ReasonNoTests
	case opts.RequireImports && candidate.Analysis.ImportFiles == 0:
		return ReasonNoImports
	default:
		return ""
	}
}
