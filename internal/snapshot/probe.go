package snapshot

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProbeStatus is the outcome of a single Probe call.
type ProbeStatus string

const (
	ProbeOK          ProbeStatus = "ok"           // recent backup returns 200
	ProbeStale       ProbeStatus = "stale"        // some backup returns 200, but it's older than staleAfter
	ProbeUnreachable ProbeStatus = "unreachable"  // no backup returns 200 within the candidate window
	ProbeNoBackups   ProbeStatus = "no_backups"   // ListBackups returned an empty slice
	ProbeBadConfig   ProbeStatus = "bad_config"   // source has an unknown IndexStrategy / missing fields
)

// ProbeResult captures everything we want to surface back to a human
// reader or to a CI workflow. Source is embedded so a JSON consumer
// can group by Domain / Network without a separate lookup.
type ProbeResult struct {
	Source        Source        `json:"source"`
	Status        ProbeStatus   `json:"status"`
	LatestBackup  string        `json:"latest_backup,omitempty"`  // e.g. "backup20260524"
	LatestAgeDays int           `json:"latest_age_days,omitempty"`
	LatencyMs     int64         `json:"latency_ms,omitempty"`
	Err           string        `json:"err,omitempty"`
}

// ProbeOptions tunes Probe behaviour. Zero values are safe defaults
// (8s per-request HTTP timeout, 7d staleness threshold, 12 candidate
// HEADs before giving up).
type ProbeOptions struct {
	HTTPTimeout    time.Duration // per-HEAD timeout. 0 → 8s
	StaleAfter     time.Duration // age beyond which a working URL is "stale". 0 → 7 days
	MaxCandidates  int           // how many backup names to HEAD-check. 0 → 12
	HTTPClient     *http.Client  // optional override (tests inject a fake)
}

// Probe HEAD-checks the latest available tarball for a single Source.
//
// For "date"-strategy mirrors (nile) we walk the generated date list
// newest-to-oldest and stop at the first 200. For "html"-strategy
// mirrors (mainnet) we scrape the index page and likewise check from
// newest to oldest.
//
// We deliberately do NOT trust the existing snapshot.LatestBackup
// helper for the "date" case — that one returns the topmost candidate
// without HEAD-checking, which would tell us nothing about whether the
// upstream is actually serving anything. Probe's whole point is to
// observe real reachability.
func Probe(ctx context.Context, s Source, opts ProbeOptions) ProbeResult {
	if opts.HTTPTimeout == 0 {
		opts.HTTPTimeout = 8 * time.Second
	}
	if opts.StaleAfter == 0 {
		opts.StaleAfter = 7 * 24 * time.Hour
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = 12
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.HTTPTimeout}
	}

	res := ProbeResult{Source: s}

	candidates, err := listProbeCandidates(ctx, s, client)
	if err != nil {
		res.Status = ProbeBadConfig
		res.Err = err.Error()
		return res
	}
	if len(candidates) == 0 {
		res.Status = ProbeNoBackups
		res.Err = "no backups returned by source"
		return res
	}

	if len(candidates) > opts.MaxCandidates {
		candidates = candidates[:opts.MaxCandidates]
	}

	// Tarball kind: we prefer probing the SAME tarball variant a user
	// would download — lite if the source has a lite tarball, otherwise
	// full. The Nile rocksdb mirror publishes only FullNode_*.tgz.
	kind := s.DBKind

	for _, backup := range candidates {
		url := TarballURL(s, backup, kind)
		if url == "" {
			continue
		}
		start := time.Now()
		ok, herr := headOK(ctx, client, url)
		res.LatencyMs = time.Since(start).Milliseconds()
		if herr != nil && res.Err == "" {
			res.Err = herr.Error()
		}
		if !ok {
			continue
		}
		// First hit wins. Decide ok vs stale based on the candidate's age.
		ageDays := backupAgeDays(backup)
		res.LatestBackup = backup
		res.LatestAgeDays = ageDays
		if time.Duration(ageDays)*24*time.Hour > opts.StaleAfter {
			res.Status = ProbeStale
			res.Err = fmt.Sprintf("most recent reachable backup is %dd old (threshold %s)",
				ageDays, opts.StaleAfter)
		} else {
			res.Status = ProbeOK
			res.Err = "" // clear any transient error from prior HEAD misses
		}
		return res
	}

	res.Status = ProbeUnreachable
	if res.Err == "" {
		res.Err = "no candidate URL returned HTTP 200"
	}
	return res
}

// listProbeCandidates returns plausible backup names newest-first for
// HEAD-checking. Thin shim over ListBackups so probe.go can be tested
// independently of the live index scrapers.
func listProbeCandidates(ctx context.Context, s Source, _ *http.Client) ([]string, error) {
	switch s.IndexStrategy {
	case "date", "html":
		return ListBackups(ctx, s)
	case "":
		return nil, fmt.Errorf("source %s has no IndexStrategy set", s.Domain)
	default:
		return nil, fmt.Errorf("source %s has unknown IndexStrategy %q", s.Domain, s.IndexStrategy)
	}
}

// headOK returns true when a HEAD against url returns a 2xx. Surfaces
// transport errors separately so the caller can keep them as context.
func headOK(ctx context.Context, client *http.Client, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

// backupAgeDays returns how many days ago a "backup20260524"-style name
// is, relative to time.Now().UTC(). Returns -1 when the name doesn't
// parse as a date — we treat that as "age unknown" rather than "fresh",
// so html-strategy mirrors that don't include a date in the directory
// name simply skip the staleness check (Status stays ProbeOK).
func backupAgeDays(backup string) int {
	name := strings.TrimPrefix(backup, "backup")
	name = strings.ReplaceAll(name, "-", "")
	if len(name) != 8 {
		return -1
	}
	t, err := time.Parse("20060102", name)
	if err != nil {
		return -1
	}
	d := time.Since(t)
	if d < 0 {
		return 0
	}
	return int(d / (24 * time.Hour))
}

// ProbeAll concurrently probes every source in sources. The slice is
// returned in the same order as the input.
func ProbeAll(ctx context.Context, sources []Source, opts ProbeOptions, parallelism int) []ProbeResult {
	if parallelism <= 0 {
		parallelism = 5
	}
	results := make([]ProbeResult, len(sources))
	sem := make(chan struct{}, parallelism)
	done := make(chan int, len(sources))

	for i, src := range sources {
		sem <- struct{}{}
		go func(idx int, s Source) {
			defer func() {
				<-sem
				done <- idx
			}()
			results[idx] = Probe(ctx, s, opts)
		}(i, src)
	}
	for range sources {
		<-done
	}
	return results
}
