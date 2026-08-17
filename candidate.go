package dependents

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/enrichment"
)

const (
	DefaultMaxPackages             = 25
	DefaultMaxDependentsPerPackage = 30
)

// PackageRef identifies a package published by the repository whose
// dependents are being discovered.
type PackageRef struct {
	Name      string
	Ecosystem string
	PURL      string
}

// Package describes a dependent package published from a candidate
// repository.
type Package struct {
	Name           string
	Ecosystem      string
	PURL           string
	RegistryURL    string
	LatestVersion  string
	Downloads      int64
	DependentRepos int
}

// RepositoryMetadata contains repository facts used by caller-selected
// filtering and ranking policies.
type RepositoryMetadata struct {
	Fork            bool
	Archived        bool
	MirrorURL       string
	SourceName      string
	PushedAt        time.Time
	StargazersCount int
	Language        string
}

// Dependent associates a package with the repository that publishes it.
type Dependent struct {
	Package
	Repository         string
	RepositoryMetadata RepositoryMetadata
}

// Group contains the packages that depend on one upstream package.
type Group struct {
	Upstream   PackageRef
	Dependents []Dependent
}

// Relationship records one package-level dependency edge inside a candidate
// repository.
type Relationship struct {
	Upstream  PackageRef
	Dependent PackageRef
}

// NativeExtension describes a detected native-extension toolchain.
type NativeExtension struct {
	Name         string
	BuildCommand string
}

// Analysis contains checkout-derived ranking signals and integrations.
type Analysis struct {
	TestFiles        int
	ImportFiles      int
	NativeExtensions []NativeExtension
}

// Candidate is one repository containing packages that depend on one or more
// upstream packages. Packages and Upstreams are deduplicated and sorted.
type Candidate struct {
	Repository         string
	Packages           []Package
	Upstreams          []PackageRef
	Relationships      []Relationship
	RepositoryMetadata RepositoryMetadata
	Downloads          int64
	DependentRepos     int
	Analysis           Analysis
	Analyzed           bool
	Commit             string
	Directory          string
}

// RepositoryDependentsClient is implemented by enrichment.EcosystemsClient.
type RepositoryDependentsClient interface {
	GetDependentsByRepositoryURL(context.Context, string, int, int) ([]enrichment.RepositoryDependents, error)
}

// DiscoverOptions bounds the packages and dependents fetched from enrichment.
type DiscoverOptions struct {
	MaxPackages             int
	MaxDependentsPerPackage int
}

// DiscoverRepository fetches dependent packages for repository and combines
// packages from the same dependent repository into one candidate.
func DiscoverRepository(ctx context.Context, client RepositoryDependentsClient, repository string, opts DiscoverOptions) ([]Candidate, error) {
	if client == nil {
		return nil, errors.New("dependents client is required")
	}
	if strings.TrimSpace(repository) == "" {
		return nil, errors.New("repository URL is required")
	}
	if opts.MaxPackages <= 0 {
		opts.MaxPackages = DefaultMaxPackages
	}
	if opts.MaxDependentsPerPackage <= 0 {
		opts.MaxDependentsPerPackage = DefaultMaxDependentsPerPackage
	}

	groups, err := client.GetDependentsByRepositoryURL(
		ctx,
		repository,
		opts.MaxPackages,
		opts.MaxDependentsPerPackage,
	)
	if err != nil {
		return nil, err
	}
	return BuildEnrichmentCandidates(groups), nil
}

// BuildEnrichmentCandidates converts enrichment results into repository-level
// candidates.
func BuildEnrichmentCandidates(groups []enrichment.RepositoryDependents) []Candidate {
	converted := make([]Group, 0, len(groups))
	for _, group := range groups {
		dependents := make([]Dependent, 0, len(group.Dependents))
		for _, dependent := range group.Dependents {
			dependents = append(dependents, Dependent{
				Package:            packageFromEnrichment(dependent),
				Repository:         dependent.Repository,
				RepositoryMetadata: repositoryMetadataFromEnrichment(dependent.RepositoryMetadata),
			})
		}
		converted = append(converted, Group{
			Upstream:   PackageRef{Name: group.PackageName, Ecosystem: group.Ecosystem, PURL: group.PURL},
			Dependents: dependents,
		})
	}
	return Build(converted)
}

// Build combines dependent packages by repository. Popularity values use the
// maximum reported by any package in the repository so a monorepo is not
// rewarded merely for publishing more packages.
func Build(groups []Group) []Candidate {
	type builder struct {
		candidate     Candidate
		packageIndex  map[string]int
		upstreams     map[string]bool
		relationships map[string]bool
	}

	builders := make(map[string]*builder)
	for _, group := range groups {
		for _, dependent := range group.Dependents {
			repository := strings.TrimSpace(dependent.Repository)
			if repository == "" {
				continue
			}
			key := canonicalRepository(repository)
			if key == "" {
				continue
			}

			b := builders[key]
			if b == nil {
				b = &builder{
					candidate:     Candidate{Repository: repository},
					packageIndex:  make(map[string]int),
					upstreams:     make(map[string]bool),
					relationships: make(map[string]bool),
				}
				builders[key] = b
			}

			pkg := dependent.Package
			mergeRepositoryMetadata(&b.candidate.RepositoryMetadata, dependent.RepositoryMetadata)
			pkgKey := packageIdentity(pkg.PURL, pkg.Ecosystem, pkg.Name)
			if i, ok := b.packageIndex[pkgKey]; ok {
				mergePackage(&b.candidate.Packages[i], pkg)
			} else {
				b.packageIndex[pkgKey] = len(b.candidate.Packages)
				b.candidate.Packages = append(b.candidate.Packages, pkg)
			}

			upstreamKey := packageIdentity(group.Upstream.PURL, group.Upstream.Ecosystem, group.Upstream.Name)
			if !b.upstreams[upstreamKey] {
				b.upstreams[upstreamKey] = true
				b.candidate.Upstreams = append(b.candidate.Upstreams, group.Upstream)
			}
			relationshipKey := upstreamKey + "\x00" + pkgKey
			if !b.relationships[relationshipKey] {
				b.relationships[relationshipKey] = true
				b.candidate.Relationships = append(b.candidate.Relationships, Relationship{
					Upstream:  group.Upstream,
					Dependent: packageRef(pkg),
				})
			}
			b.candidate.Downloads = max(b.candidate.Downloads, pkg.Downloads)
			b.candidate.DependentRepos = max(b.candidate.DependentRepos, pkg.DependentRepos)
		}
	}

	candidates := make([]Candidate, 0, len(builders))
	for _, b := range builders {
		sort.Slice(b.candidate.Packages, func(i, j int) bool {
			return packageIdentity(b.candidate.Packages[i].PURL, b.candidate.Packages[i].Ecosystem, b.candidate.Packages[i].Name) <
				packageIdentity(b.candidate.Packages[j].PURL, b.candidate.Packages[j].Ecosystem, b.candidate.Packages[j].Name)
		})
		sort.Slice(b.candidate.Upstreams, func(i, j int) bool {
			return packageIdentity(b.candidate.Upstreams[i].PURL, b.candidate.Upstreams[i].Ecosystem, b.candidate.Upstreams[i].Name) <
				packageIdentity(b.candidate.Upstreams[j].PURL, b.candidate.Upstreams[j].Ecosystem, b.candidate.Upstreams[j].Name)
		})
		sort.Slice(b.candidate.Relationships, func(i, j int) bool {
			left := relationshipIdentity(b.candidate.Relationships[i])
			right := relationshipIdentity(b.candidate.Relationships[j])
			return left < right
		})
		candidates = append(candidates, b.candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return canonicalRepository(candidates[i].Repository) < canonicalRepository(candidates[j].Repository)
	})
	return candidates
}

func packageRef(pkg Package) PackageRef {
	return PackageRef{Name: pkg.Name, Ecosystem: pkg.Ecosystem, PURL: pkg.PURL}
}

func relationshipIdentity(relationship Relationship) string {
	return packageIdentity(relationship.Upstream.PURL, relationship.Upstream.Ecosystem, relationship.Upstream.Name) + "\x00" +
		packageIdentity(relationship.Dependent.PURL, relationship.Dependent.Ecosystem, relationship.Dependent.Name)
}

// ExcludeRepositories returns a copy without candidates whose canonical
// repository URL appears in repositories.
func ExcludeRepositories(candidates []Candidate, repositories ...string) []Candidate {
	excluded := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		if key := canonicalRepository(repository); key != "" {
			excluded[key] = true
		}
	}
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !excluded[canonicalRepository(candidate.Repository)] {
			out = append(out, candidate)
		}
	}
	return out
}

func packageFromEnrichment(pkg enrichment.DependentPackage) Package {
	return Package{
		Name:           pkg.Name,
		Ecosystem:      pkg.Ecosystem,
		PURL:           pkg.PURL,
		RegistryURL:    pkg.RegistryURL,
		LatestVersion:  pkg.LatestVersion,
		Downloads:      int64(pkg.Downloads),
		DependentRepos: pkg.DependentReposCount,
	}
}

func repositoryMetadataFromEnrichment(metadata enrichment.RepositoryMetadata) RepositoryMetadata {
	return RepositoryMetadata{
		Fork:            metadata.Fork,
		Archived:        metadata.Archived,
		MirrorURL:       metadata.MirrorURL,
		SourceName:      metadata.SourceName,
		PushedAt:        metadata.PushedAt,
		StargazersCount: metadata.StargazersCount,
		Language:        metadata.Language,
	}
}

func mergePackage(existing *Package, incoming Package) {
	existing.Downloads = max(existing.Downloads, incoming.Downloads)
	existing.DependentRepos = max(existing.DependentRepos, incoming.DependentRepos)
	if existing.RegistryURL == "" {
		existing.RegistryURL = incoming.RegistryURL
	}
	if existing.LatestVersion == "" {
		existing.LatestVersion = incoming.LatestVersion
	}
}

func mergeRepositoryMetadata(existing *RepositoryMetadata, incoming RepositoryMetadata) {
	existing.Fork = existing.Fork || incoming.Fork
	existing.Archived = existing.Archived || incoming.Archived
	if existing.MirrorURL == "" {
		existing.MirrorURL = incoming.MirrorURL
	}
	if existing.SourceName == "" {
		existing.SourceName = incoming.SourceName
	}
	if incoming.PushedAt.After(existing.PushedAt) {
		existing.PushedAt = incoming.PushedAt
	}
	existing.StargazersCount = max(existing.StargazersCount, incoming.StargazersCount)
	if existing.Language == "" {
		existing.Language = incoming.Language
	}
}

func packageIdentity(purl, ecosystem, name string) string {
	if purl != "" {
		return "purl:" + purl
	}
	return "name:" + strings.ToLower(ecosystem) + "\x00" + name
}

func canonicalRepository(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimRepositorySuffix(raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = trimRepositorySuffix(parsed.Path)
	return parsed.String()
}

func trimRepositorySuffix(value string) string {
	value = strings.TrimRight(value, "/")
	if strings.HasSuffix(strings.ToLower(value), ".git") {
		value = value[:len(value)-len(".git")]
	}
	return value
}
