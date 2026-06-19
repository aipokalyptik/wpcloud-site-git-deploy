package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const SchemaVersion = 1

type Phase struct {
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
}

type Host struct {
	Hostname string `json:"hostname"`
	PID      int    `json:"pid"`
}

type ClaimsStats struct {
	New          int `json:"new"`
	Old          int `json:"old"`
	Materialized int `json:"materialized"`
	Removed      int `json:"removed"`
	Created      int `json:"created"`
	Exchanged    int `json:"exchanged"`
	Reconciled   int `json:"reconciled"`
}

type DocrootScanStats struct {
	Entries          int `json:"entries"`
	Symlinks         int `json:"symlinks"`
	Boundaries       int `json:"boundaries"`
	ProtectedAnchors int `json:"protected_anchors"`
}

type ReleaseStats struct {
	Retained int `json:"retained"`
	Pruned   int `json:"pruned"`
	Keep     int `json:"keep"`
}

type StagingSweepStats struct {
	StaleWorktreesRemoved int `json:"stale_worktrees_removed"`
	StaleIncomingRemoved  int `json:"stale_incoming_removed"`
}

type GitStats struct {
	UsedLFS    bool `json:"used_lfs"`
	LFSPaths   int  `json:"lfs_paths"`
	Submodules int  `json:"submodules"`
}

type RsyncStats struct {
	FilesTransferred int     `json:"files_transferred"`
	TotalFiles       int     `json:"total_files"`
	LiteralDataBytes int64   `json:"literal_data_bytes"`
	MatchedDataBytes int64   `json:"matched_data_bytes"`
	TotalSizeBytes   int64   `json:"total_size_bytes"`
	Speedup          float64 `json:"speedup"`
}

type RuntimeStats struct {
	PeakHeapBytes uint64 `json:"peak_heap_bytes"`
	NumGC         uint32 `json:"num_gc"`
}

type Stats struct {
	Claims       ClaimsStats       `json:"claims"`
	DocrootScan  DocrootScanStats  `json:"docroot_scan"`
	Releases     ReleaseStats      `json:"releases"`
	StagingSweep StagingSweepStats `json:"staging_sweep"`
	Git          GitStats          `json:"git"`
	Rsync        *RsyncStats       `json:"rsync,omitempty"`
	Runtime      RuntimeStats      `json:"runtime"`
}

type Record struct {
	SchemaVersion int       `json:"schema_version"`
	ToolVersion   string    `json:"tool_version"`
	Name          string    `json:"name"`
	DeploymentID  string    `json:"deployment_id"`
	Status        string    `json:"status"`
	ReleaseID     string    `json:"release_id"`
	Commit        string    `json:"commit"`
	RefMode       string    `json:"ref_mode"`
	RefValue      string    `json:"ref_value"`
	DeployRoot    string    `json:"deploy_root"`
	Force         bool      `json:"force"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
	FailedPhase   *string   `json:"failed_phase"`
	Error         *string   `json:"error"`
	Host          Host      `json:"host"`
	Phases        []Phase   `json:"phases"`
	Stats         Stats     `json:"stats"`
}

type Collector struct {
	record      Record
	started     time.Time
	peakHeap    uint64
	finalized   bool
	reportPath  string
	runsPath    string
	sidecarPath string
	latestPath  string
	retainRuns  int
}

type Options struct {
	ToolVersion  string
	Name         string
	DeploymentID string
	RefMode      string
	RefValue     string
	DeployRoot   string
	Force        bool
	RunsPath     string
	SidecarPath  string
	LatestPath   string
	RetainRuns   int
}

func New(options Options) *Collector {
	started := time.Now()
	hostname, _ := os.Hostname()
	retainRuns := options.RetainRuns
	if retainRuns <= 0 {
		retainRuns = 200
	}
	collector := &Collector{
		started:     started,
		runsPath:    options.RunsPath,
		sidecarPath: options.SidecarPath,
		latestPath:  options.LatestPath,
		retainRuns:  retainRuns,
		record: Record{
			SchemaVersion: SchemaVersion,
			ToolVersion:   options.ToolVersion,
			Name:          options.Name,
			DeploymentID:  options.DeploymentID,
			RefMode:       options.RefMode,
			RefValue:      options.RefValue,
			DeployRoot:    options.DeployRoot,
			Force:         options.Force,
			StartedAt:     started.UTC(),
			Host:          Host{Hostname: hostname, PID: os.Getpid()},
		},
	}
	collector.SampleRuntime()
	return collector
}

func (collector *Collector) Phase(name string) func() {
	started := time.Now()
	return func() {
		collector.SampleRuntime()
		collector.record.Phases = append(collector.record.Phases, Phase{
			Name:       name,
			DurationMS: millisecondsSince(started),
		})
	}
}

func (collector *Collector) SetRelease(releaseID string) {
	collector.record.ReleaseID = releaseID
	if collector.sidecarPath != "" {
		collector.reportPath = collector.sidecarPath
	}
}

func (collector *Collector) SetSidecar(path string) {
	collector.sidecarPath = path
	if collector.record.ReleaseID != "" && path != "" {
		collector.reportPath = path
	}
}

func (collector *Collector) SetCommit(commit string) {
	collector.record.Commit = commit
}

func (collector *Collector) SetFailedPhase(name string) {
	collector.record.FailedPhase = &name
}

func (collector *Collector) SetRsync(stats *RsyncStats) {
	collector.record.Stats.Rsync = stats
}

func (collector *Collector) Stats() *Stats {
	return &collector.record.Stats
}

func (collector *Collector) Record() Record {
	return collector.record
}

func (collector *Collector) ReportPath() string {
	if collector.reportPath != "" {
		return collector.reportPath
	}
	return collector.runsPath
}

func (collector *Collector) SampleRuntime() {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if memory.HeapAlloc > collector.peakHeap {
		collector.peakHeap = memory.HeapAlloc
	}
	collector.record.Stats.Runtime = RuntimeStats{
		PeakHeapBytes: collector.peakHeap,
		NumGC:         memory.NumGC,
	}
}

func (collector *Collector) Finish(status string, err error) error {
	if collector.finalized {
		return nil
	}
	collector.finalized = true
	collector.SampleRuntime()
	finished := time.Now()
	collector.record.Status = status
	collector.record.FinishedAt = finished.UTC()
	collector.record.DurationMS = millisecondsSince(collector.started)
	if err != nil {
		errText := err.Error()
		collector.record.Error = &errText
	}
	if collector.reportPath == "" {
		collector.reportPath = collector.runsPath
	}
	if collector.runsPath != "" {
		if err := AppendBoundedJSONL(collector.runsPath, collector.record, collector.retainRuns); err != nil {
			return err
		}
	}
	if status == "success" && collector.sidecarPath != "" {
		if err := WriteJSON(collector.sidecarPath, collector.record); err != nil {
			return err
		}
		if collector.latestPath != "" {
			if err := WriteJSON(collector.latestPath, collector.record); err != nil {
				return err
			}
		}
	}
	return nil
}

func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func AppendBoundedJSONL(path string, value any, retain int) error {
	if retain <= 0 {
		retain = 200
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	file, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		scanner := bufio.NewScanner(file)
		// Reports are small, but allow a generous line size so a large command
		// error does not make history maintenance fail.
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			if scanner.Text() != "" {
				lines = append(lines, scanner.Text())
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			file.Close()
			return scanErr
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	lines = append(lines, string(data))
	if len(lines) > retain {
		lines = lines[len(lines)-retain:]
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	writer := bufio.NewWriter(tmp)
	for _, line := range lines {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func millisecondsSince(started time.Time) int64 {
	elapsed := time.Since(started)
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}
