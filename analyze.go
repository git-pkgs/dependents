package dependents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/git-pkgs/brief"
	"github.com/git-pkgs/brief/detect"
	"github.com/git-pkgs/brief/kb"
)

const (
	analyzeDirPerm os.FileMode = 0o755
	maxScanSize                = 256 << 10
)

// AnalyzeOptions controls checkout analysis.
type AnalyzeOptions struct {
	Upstreams              []string
	Workdir                string
	Checkout               Checkout
	Keep                   bool
	DetectNativeExtensions bool
}

// AnalysisFailure records one candidate that could not be checked out or
// scanned. The candidate remains in AnalysisResult.Candidates unchanged.
type AnalysisFailure struct {
	Repository string
	Err        error
}

// AnalysisResult contains every candidate and any per-repository failures.
type AnalysisResult struct {
	Candidates []Candidate
	Failures   []AnalysisFailure
}

// Analyze checks out and scans each candidate. Individual repository failures
// are collected without stopping the remaining candidates.
func Analyze(ctx context.Context, candidates []Candidate, opts AnalyzeOptions) (AnalysisResult, error) {
	result := AnalysisResult{Candidates: append([]Candidate(nil), candidates...)}
	checkout := opts.Checkout
	if checkout == nil {
		checkout = CloneCheckout{}
	}

	workdir := opts.Workdir
	persistent := workdir != "" || opts.Keep
	if workdir == "" {
		temporary, err := os.MkdirTemp("", "dependents-analyze-")
		if err != nil {
			return result, err
		}
		workdir = temporary
		if !opts.Keep {
			defer func() { _ = os.RemoveAll(temporary) }()
		}
	} else if err := os.MkdirAll(workdir, analyzeDirPerm); err != nil {
		return result, err
	}

	for i := range result.Candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candidate := &result.Candidates[i]
		destination := filepath.Join(workdir, candidateDirectory(candidate.Repository))
		commit, err := checkout.Prepare(ctx, candidate.Repository, destination)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			result.Failures = append(result.Failures, AnalysisFailure{Repository: candidate.Repository, Err: err})
			continue
		}
		analysis, err := AnalyzeDirectory(destination, opts.Upstreams)
		if err != nil {
			result.Failures = append(result.Failures, AnalysisFailure{Repository: candidate.Repository, Err: err})
			continue
		}
		if opts.DetectNativeExtensions {
			analysis.NativeExtensions, err = detectNativeExtensions(destination)
			if err != nil {
				result.Failures = append(result.Failures, AnalysisFailure{Repository: candidate.Repository, Err: err})
				continue
			}
		}
		candidate.Analysis = analysis
		candidate.Analyzed = true
		candidate.Commit = commit
		if persistent {
			candidate.Directory = destination
		}
	}
	return result, nil
}

// AnalyzeDirectory counts conventional test files and files whose content
// mentions at least one upstream package name.
func AnalyzeDirectory(root string, upstreams []string) (Analysis, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Analysis{}, err
	}
	if !info.IsDir() {
		return Analysis{}, fmt.Errorf("analysis root %q is not a directory", root)
	}

	needles := make([][]byte, 0, len(upstreams))
	seen := make(map[string]bool, len(upstreams))
	for _, upstream := range upstreams {
		upstream = strings.TrimSpace(upstream)
		if upstream != "" && !seen[upstream] {
			seen[upstream] = true
			needles = append(needles, []byte(upstream))
		}
	}

	var analysis Analysis
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && skipDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if inTestDir(path, root, testDirs()) || isTestFile(name) {
			analysis.TestFiles++
		}
		if fileMentionsAny(path, entry, needles) {
			analysis.ImportFiles++
		}
		return nil
	})
	if err != nil {
		return Analysis{}, err
	}
	return analysis, nil
}

func candidateDirectory(repository string) string {
	sum := sha256.Sum256([]byte(canonicalRepository(repository)))
	return hex.EncodeToString(sum[:])
}

var loadKnowledge = sync.OnceValues(func() (*kb.KnowledgeBase, error) {
	return kb.Load(brief.KnowledgeFS)
})

var testDirs = sync.OnceValue(func() map[string]bool {
	dirs := make(map[string]bool)
	knowledge, err := loadKnowledge()
	if err != nil {
		return dirs
	}
	for _, dir := range knowledge.Layouts.Layout.TestDirs {
		dirs[dir] = true
	}
	return dirs
})

func detectNativeExtensions(root string) ([]NativeExtension, error) {
	knowledge, err := loadKnowledge()
	if err != nil {
		return nil, err
	}
	report, err := detect.New(knowledge, root).Run()
	if err != nil {
		return nil, err
	}

	detections := report.Tools["native_extension"]
	extensions := make([]NativeExtension, 0, len(detections))
	for _, detection := range detections {
		extension := NativeExtension{Name: detection.Name}
		if detection.Command != nil {
			extension.BuildCommand = detection.Command.Run
		}
		extensions = append(extensions, extension)
	}
	sort.Slice(extensions, func(i, j int) bool {
		return extensions[i].Name < extensions[j].Name
	})
	return extensions, nil
}

func isTestFile(base string) bool {
	stem, extension, ok := strings.Cut(base, ".")
	if !ok {
		return false
	}
	if strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_spec") || strings.HasPrefix(stem, "test_") {
		return true
	}
	return strings.HasPrefix(extension, "test.") || strings.HasPrefix(extension, "spec.")
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata", "node_modules", "target", "dist", "build":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}

func inTestDir(path, root string, directories map[string]bool) bool {
	relative, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return false
	}
	for segment := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if directories[segment] {
			return true
		}
	}
	return false
}

var nonSourceExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".ico": true, ".webp": true, ".pdf": true, ".woff": true, ".woff2": true,
	".ttf": true, ".eot": true, ".otf": true, ".mp3": true, ".mp4": true,
	".webm": true, ".ogg": true, ".wav": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true,
	".7z": true, ".rar": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true,
	".o": true, ".class": true, ".jar": true, ".war": true, ".wasm": true,
	".pyc": true, ".pyo": true,
	".lock": true, ".sum": true,
}

func fileMentionsAny(path string, entry fs.DirEntry, needles [][]byte) bool {
	if len(needles) == 0 {
		return false
	}
	extension := strings.ToLower(filepath.Ext(path))
	if nonSourceExt[extension] {
		return false
	}
	lower := strings.ToLower(entry.Name())
	if strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.css") {
		return false
	}
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScanSize {
		return false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, needle := range needles {
		if bytes.Contains(content, needle) {
			return true
		}
	}
	return false
}
