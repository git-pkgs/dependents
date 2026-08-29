package dependents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnalyzeDirectory(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"src/a.go":             `import "example.com/upstream/one"`,
		"src/b.go":             `import "example.com/upstream/two"`,
		"src/a_test.go":        `import "testing"`,
		"tests/integration.go": `import "example.com/upstream/one"`,
		"vendor/x/x_test.go":   `import "example.com/upstream/one"`,
		"testdata/y_test.go":   `import "example.com/upstream/one"`,
		"go.sum":               "example.com/upstream/one v1.0.0 h1:abc",
		"assets/logo.png":      "example.com/upstream/one",
	})

	got, err := AnalyzeDirectory(root, []string{"example.com/upstream/one", "example.com/upstream/two", ""})
	if err != nil {
		t.Fatalf("AnalyzeDirectory: %v", err)
	}
	if got.TestFiles != 2 || got.ImportFiles != 3 {
		t.Errorf("analysis = %+v, want 2 test files and 3 import files", got)
	}
}

func TestAnalyzeDirectoryRejectsInvalidRoot(t *testing.T) {
	if _, err := AnalyzeDirectory(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing root error = nil")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeDirectory(file, nil); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file root error = %v", err)
	}
}

func TestAnalyzeDirectorySkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.go")
	if err := os.WriteFile(external, []byte(`import "example.com/upstream"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "linked_test.go")); err != nil {
		t.Fatal(err)
	}

	got, err := AnalyzeDirectory(root, []string{"example.com/upstream"})
	if err != nil {
		t.Fatalf("AnalyzeDirectory: %v", err)
	}
	if !reflect.DeepEqual(got, Analysis{}) {
		t.Errorf("analysis = %+v, want symlink excluded", got)
	}
}

func TestAnalyzeKeepsFailuresAndPersistentDirectories(t *testing.T) {
	workdir := t.TempDir()
	checkout := CheckoutFunc(func(_ context.Context, repository, destination string) (string, error) {
		if strings.HasSuffix(repository, "/failed") {
			return "", errors.New("clone failed")
		}
		writeTree(t, destination, map[string]string{
			"main.go":      `import "example.com/upstream"`,
			"main_test.go": `package main`,
		})
		return "abc123", nil
	})
	candidates := []Candidate{
		{Repository: "https://example.com/good"},
		{Repository: "https://example.com/failed"},
	}

	result, err := Analyze(context.Background(), candidates, AnalyzeOptions{
		Upstreams: []string{"example.com/upstream"},
		Workdir:   workdir,
		Checkout:  checkout,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(result.Candidates) != 2 || len(result.Failures) != 1 {
		t.Fatalf("result = %+v", result)
	}
	good := result.Candidates[0]
	if !good.Analyzed || good.Commit != "abc123" || !reflect.DeepEqual(good.Analysis, Analysis{TestFiles: 1, ImportFiles: 1}) {
		t.Errorf("good candidate = %+v", good)
	}
	if good.Directory == "" {
		t.Fatal("persistent checkout directory is empty")
	}
	if _, err := os.Stat(good.Directory); err != nil {
		t.Errorf("persistent checkout: %v", err)
	}
	if result.Candidates[1].Analyzed {
		t.Errorf("failed candidate = %+v", result.Candidates[1])
	}
	if result.Failures[0].Repository != "https://example.com/failed" || !strings.Contains(result.Failures[0].Err.Error(), "clone failed") {
		t.Errorf("failure = %+v", result.Failures[0])
	}
	if candidates[0].Analyzed || candidates[0].Directory != "" {
		t.Error("Analyze modified its input")
	}
}

func TestAnalyzeDetectsNativeExtensions(t *testing.T) {
	checkout := CheckoutFunc(func(_ context.Context, _ string, destination string) (string, error) {
		writeTree(t, destination, map[string]string{
			"Cargo.toml": `[package]
name = "native-package"
version = "0.1.0"

[dependencies]
rb-sys = "0.9"
`,
			"Gemfile": `source "https://rubygems.org"
gem "rb_sys"
`,
			"pyproject.toml": `[build-system]
requires = ["maturin>=1.0,<2.0"]
build-backend = "maturin"

[project]
name = "native-package"
version = "0.1.0"

[tool.maturin]
bindings = "pyo3"
`,
		})
		return "abc123", nil
	})

	result, err := Analyze(context.Background(), []Candidate{{Repository: "https://example.com/native"}}, AnalyzeOptions{
		Checkout:               checkout,
		DetectNativeExtensions: true,
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("failures = %+v", result.Failures)
	}
	want := []NativeExtension{
		{Name: "Maturin", BuildCommand: "maturin develop"},
		{Name: "rb-sys", BuildCommand: "bundle exec rake compile"},
	}
	if got := result.Candidates[0].Analysis.NativeExtensions; !reflect.DeepEqual(got, want) {
		t.Errorf("native extensions = %+v, want %+v", got, want)
	}
}

func TestAnalyzeStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Analyze(ctx, []Candidate{{Repository: "https://example.com/app"}}, AnalyzeOptions{Checkout: CheckoutFunc(func(context.Context, string, string) (string, error) {
		t.Fatal("checkout called after cancellation")
		return "", nil
	})})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestAnalyzeRemovesTemporaryCheckout(t *testing.T) {
	var destination string
	result, err := Analyze(context.Background(), []Candidate{{Repository: "https://example.com/app"}}, AnalyzeOptions{
		Upstreams: []string{"example.com/upstream"},
		Checkout: CheckoutFunc(func(_ context.Context, _ string, dest string) (string, error) {
			destination = dest
			writeTree(t, dest, map[string]string{"main.go": `import "example.com/upstream"`})
			return "abc123", nil
		}),
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Candidates[0].Directory != "" {
		t.Errorf("temporary directory exposed as %q", result.Candidates[0].Directory)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary checkout still exists: %v", err)
	}
}

func TestCheckoutAdaptersRequireConfiguration(t *testing.T) {
	if _, err := (CheckoutFunc(nil)).Prepare(context.Background(), "https://example.com/app", t.TempDir()); err == nil {
		t.Fatal("nil checkout function error = nil")
	}
	if _, err := (CacheCheckout{}).Prepare(context.Background(), "https://example.com/app", t.TempDir()); err == nil {
		t.Fatal("nil clone cache error = nil")
	}
}
