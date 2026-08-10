package dependents

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/git-pkgs/enrichment"
)

func TestBuildDeduplicatesRepositoriesAndPreservesRelationships(t *testing.T) {
	groups := []Group{
		{
			Upstream: PackageRef{Name: "library-a", Ecosystem: "npm", PURL: "pkg:npm/library-a"},
			Dependents: []Dependent{
				{
					Package:    Package{Name: "app-a", Ecosystem: "npm", PURL: "pkg:npm/app-a", Downloads: 100},
					Repository: "https://github.com/acme/app.git",
				},
				{Package: Package{Name: "no-repository", Ecosystem: "npm", PURL: "pkg:npm/no-repository"}},
			},
		},
		{
			Upstream: PackageRef{Name: "library-b", Ecosystem: "npm", PURL: "pkg:npm/library-b"},
			Dependents: []Dependent{
				{
					Package: Package{
						Name: "app-a", Ecosystem: "npm", PURL: "pkg:npm/app-a",
						Downloads: 150, DependentRepos: 12, LatestVersion: "2.0.0",
					},
					Repository: "https://github.com/acme/app/",
				},
				{
					Package:    Package{Name: "app-b", Ecosystem: "npm", PURL: "pkg:npm/app-b", Downloads: 300, DependentRepos: 7},
					Repository: "https://github.com/acme/app",
					RepositoryMetadata: RepositoryMetadata{
						PushedAt:        time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC),
						StargazersCount: 200,
						Language:        "Go",
					},
				},
				{
					Package:    Package{Name: "other", Ecosystem: "npm", PURL: "pkg:npm/other", Downloads: 10},
					Repository: "https://github.com/acme/other",
				},
			},
		},
	}

	got := Build(groups)
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	app := got[0]
	if app.Repository != "https://github.com/acme/app.git" {
		t.Errorf("repository = %q, want first observed URL", app.Repository)
	}
	if len(app.Packages) != 2 || len(app.Upstreams) != 2 || len(app.Relationships) != 3 {
		t.Fatalf("app candidate = %+v", app)
	}
	if app.Downloads != 300 || app.DependentRepos != 12 {
		t.Errorf("popularity = downloads %d, dependent repos %d", app.Downloads, app.DependentRepos)
	}
	if app.RepositoryMetadata.StargazersCount != 200 || app.RepositoryMetadata.Language != "Go" || app.RepositoryMetadata.PushedAt.IsZero() {
		t.Errorf("repository metadata = %+v", app.RepositoryMetadata)
	}
	if app.Packages[0].Name != "app-a" || app.Packages[0].LatestVersion != "2.0.0" || app.Packages[0].Downloads != 150 {
		t.Errorf("merged package = %+v", app.Packages[0])
	}

	wantRelationships := []Relationship{
		{
			Upstream:  PackageRef{Name: "library-a", Ecosystem: "npm", PURL: "pkg:npm/library-a"},
			Dependent: PackageRef{Name: "app-a", Ecosystem: "npm", PURL: "pkg:npm/app-a"},
		},
		{
			Upstream:  PackageRef{Name: "library-b", Ecosystem: "npm", PURL: "pkg:npm/library-b"},
			Dependent: PackageRef{Name: "app-a", Ecosystem: "npm", PURL: "pkg:npm/app-a"},
		},
		{
			Upstream:  PackageRef{Name: "library-b", Ecosystem: "npm", PURL: "pkg:npm/library-b"},
			Dependent: PackageRef{Name: "app-b", Ecosystem: "npm", PURL: "pkg:npm/app-b"},
		},
	}
	if !reflect.DeepEqual(app.Relationships, wantRelationships) {
		t.Errorf("relationships = %+v, want %+v", app.Relationships, wantRelationships)
	}
}

func TestBuildEnrichmentCandidates(t *testing.T) {
	groups := []enrichment.RepositoryDependents{{
		PackageName: "github.com/acme/library",
		Ecosystem:   "go",
		PURL:        "pkg:golang/github.com/acme/library",
		Dependents: []enrichment.DependentPackage{{
			Name: "github.com/acme/app", Ecosystem: "go",
			PURL: "pkg:golang/github.com/acme/app", Repository: "https://github.com/acme/app",
			Downloads: 9, DependentReposCount: 40, RegistryURL: "https://pkg.go.dev/github.com/acme/app",
			LatestVersion: "v1.2.3",
			RepositoryMetadata: enrichment.RepositoryMetadata{
				PushedAt:        time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC),
				StargazersCount: 400,
				Language:        "Go",
			},
		}},
	}}

	got := BuildEnrichmentCandidates(groups)
	if len(got) != 1 || len(got[0].Packages) != 1 {
		t.Fatalf("candidates = %+v", got)
	}
	pkg := got[0].Packages[0]
	if pkg.Downloads != 9 || pkg.DependentRepos != 40 || pkg.LatestVersion != "v1.2.3" {
		t.Errorf("package = %+v", pkg)
	}
	if got[0].RepositoryMetadata.StargazersCount != 400 || got[0].RepositoryMetadata.Language != "Go" {
		t.Errorf("repository metadata = %+v", got[0].RepositoryMetadata)
	}
}

func TestExcludeRepositoriesUsesCanonicalURL(t *testing.T) {
	candidates := []Candidate{
		{Repository: "https://github.com/acme/library.git"},
		{Repository: "https://github.com/acme/app"},
	}
	got := ExcludeRepositories(candidates, "HTTPS://GITHUB.COM/acme/library/")
	if len(got) != 1 || got[0].Repository != "https://github.com/acme/app" {
		t.Fatalf("candidates = %+v", got)
	}
}

type fakeRepositoryClient struct {
	repository    string
	maxPackages   int
	maxDependents int
	groups        []enrichment.RepositoryDependents
	err           error
}

func (f *fakeRepositoryClient) GetDependentsByRepositoryURL(_ context.Context, repository string, maxPackages, maxDependents int) ([]enrichment.RepositoryDependents, error) {
	f.repository = repository
	f.maxPackages = maxPackages
	f.maxDependents = maxDependents
	return f.groups, f.err
}

func TestDiscoverRepository(t *testing.T) {
	client := &fakeRepositoryClient{groups: []enrichment.RepositoryDependents{{
		PackageName: "library",
		Dependents: []enrichment.DependentPackage{{
			Name: "app", Repository: "https://github.com/acme/app",
		}},
	}}}
	got, err := DiscoverRepository(context.Background(), client, "https://github.com/acme/library", DiscoverOptions{})
	if err != nil {
		t.Fatalf("DiscoverRepository: %v", err)
	}
	if len(got) != 1 || got[0].Repository != "https://github.com/acme/app" {
		t.Fatalf("candidates = %+v", got)
	}
	if client.maxPackages != DefaultMaxPackages || client.maxDependents != DefaultMaxDependentsPerPackage {
		t.Errorf("limits = %d, %d", client.maxPackages, client.maxDependents)
	}
}

func TestDiscoverRepositoryErrors(t *testing.T) {
	if _, err := DiscoverRepository(context.Background(), nil, "https://github.com/acme/library", DiscoverOptions{}); err == nil {
		t.Fatal("nil client error = nil")
	}
	if _, err := DiscoverRepository(context.Background(), &fakeRepositoryClient{}, "", DiscoverOptions{}); err == nil {
		t.Fatal("empty repository error = nil")
	}
	want := errors.New("fetch failed")
	_, err := DiscoverRepository(context.Background(), &fakeRepositoryClient{err: want}, "https://github.com/acme/library", DiscoverOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
