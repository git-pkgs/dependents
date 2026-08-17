# dependents

Go library for finding and ranking repositories that depend on a package. It uses [enrichment](https://github.com/git-pkgs/enrichment) for dependent package metadata and [clone](https://github.com/git-pkgs/clone) for local checkouts. Candidates are deduplicated by repository while retaining the package-level dependency relationships. The package supports Go 1.26 or later.

## Install

```
go get github.com/git-pkgs/dependents
```

## Usage

```go
client, err := enrichment.NewEcosystemsClient()
if err != nil {
    log.Fatal(err)
}

candidates, err := dependents.DiscoverRepository(ctx, client,
    "https://github.com/acme/library", dependents.DiscoverOptions{})
if err != nil {
    log.Fatal(err)
}

kept, rejected := dependents.Filter(candidates, dependents.FilterOptions{
    ExcludeForks:    true,
    ExcludeArchived: true,
    ExcludeMirrors:  true,
    MaxAge:          2 * 365 * 24 * time.Hour,
})
for _, rejection := range rejected {
    log.Printf("skip %s: %s", rejection.Candidate.Repository, rejection.Reason)
}
ranked := dependents.Rank(kept, 10, nil)
```

`Build` accepts the package's neutral `Group` type when the caller already has dependent data. `BuildEnrichmentCandidates` adapts `enrichment.RepositoryDependents` directly.

`DefaultScore` favors source references and tests after checkout analysis. Callers can pass another `ScoreFunc` to `Rank`, so security exposure and contract-test selection can use different policies.

`Analyze` accepts any `Checkout`. `CloneCheckout` uses a direct checkout and supports full history for Hyrum, while `CacheCheckout` copies from a persistent `git-pkgs/clone` cache for Scrutineer. A caller that supplies `Workdir` or sets `Keep` receives the checkout path on each analyzed candidate for follow-up work. Set `DetectNativeExtensions` to record native-extension toolchains and their build commands, including Maturin, napi-rs, Neon, rb-sys, Rustler, and setuptools-rust.

After analysis, `FilterOptions.RequireTests` and `RequireImports` reproduce the contract-test eligibility used by downstream. Scrutineer can require upstream references without excluding repositories that have no conventional test files.

## License

MIT
