package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/trace"
)

func (s *Server) handleBenchmarks(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		dir = defaultBenchmarkRunsDir()
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(config.BillyHomeDir(), dir)
	}
	runs, err := listBenchmarkRuns(dir, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, BenchmarkListResponse{
		Dir:  filepath.Clean(dir),
		Runs: runs,
	})
}

func defaultBenchmarkRunsDir() string {
	return filepath.Join(config.BillyHomeDir(), "bench-runs")
}

func listBenchmarkRuns(root string, limit int) ([]BenchmarkRunSummary, error) {
	root = filepath.Clean(root)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []BenchmarkRunSummary
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err == nil && strings.Count(rel, string(os.PathSeparator)) >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "-manifest.json") {
			return nil
		}
		run, err := readBenchmarkRunSummary(path)
		if err == nil && run.RunID != "" {
			runs = append(runs, run)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func readBenchmarkRunSummary(manifestPath string) (BenchmarkRunSummary, error) {
	bytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return BenchmarkRunSummary{}, err
	}
	var manifest trace.Manifest
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return BenchmarkRunSummary{}, err
	}
	manifestDir := filepath.Dir(manifestPath)
	resultsPath := resolveBenchmarkArtifactPath(manifestDir, manifest.ResultsJSONL)
	eventsPath := resolveBenchmarkArtifactPath(manifestDir, manifest.EventsJSONL)
	payloadsDir := resolveBenchmarkArtifactPath(manifestDir, manifest.PayloadsDir)
	return BenchmarkRunSummary{
		RunID:           manifest.RunID,
		CreatedAt:       manifest.CreatedAt,
		Harness:         manifest.Harness,
		ProfileHash:     manifest.ProfileHash,
		TasksPath:       manifest.TasksPath,
		TaskCount:       manifest.TaskCount,
		ManifestJSON:    filepath.Clean(manifestPath),
		ResultsJSONL:    resultsPath,
		EventsJSONL:     eventsPath,
		PayloadsDir:     payloadsDir,
		ResultsPresent:  fileExists(resultsPath),
		EventsPresent:   fileExists(eventsPath),
		PayloadsPresent: dirExists(payloadsDir),
	}, nil
}

func resolveBenchmarkArtifactPath(manifestDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	candidates := []string{
		filepath.Clean(path),
		filepath.Join(manifestDir, filepath.Base(path)),
		filepath.Join(manifestDir, path),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) || dirExists(candidate) {
			return absBenchmarkPath(candidate)
		}
	}
	return filepath.Join(manifestDir, filepath.Base(path))
}

func absBenchmarkPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
