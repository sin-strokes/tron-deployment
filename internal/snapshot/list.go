package snapshot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// backupRE recognises directory names like `backup20250101` or
// `backup2025-01-01` (nile uses ISO-style with dashes). The capture
// group is the date string used for sorting.
var backupRE = regexp.MustCompile(`backup(\d{4}-?\d{2}-?\d{2})`)

// ListBackups returns the available backup names for a source, sorted
// newest-first. Mainnet mirrors expose an Apache/nginx index listing that
// we scrape; the Nile S3 endpoint has no listing endpoint, so we generate
// a plausible date list and let the caller HEAD-check whichever it picks.
func ListBackups(ctx context.Context, s Source) ([]string, error) {
	switch s.IndexStrategy {
	case "html":
		return listFromHTMLIndex(ctx, s.BaseURL)
	case "date":
		return generateDateList(), nil
	default:
		return nil, fmt.Errorf("unknown index strategy %q for %s", s.IndexStrategy, s.Domain)
	}
}

// LatestBackup returns the most recent backup name, or "" if none could
// be determined.
func LatestBackup(ctx context.Context, s Source) (string, error) {
	backups, err := ListBackups(ctx, s)
	if err != nil {
		return "", err
	}
	if len(backups) == 0 {
		return "", fmt.Errorf("no backups found at %s", s.Domain)
	}
	return backups[0], nil
}

// listFromHTMLIndex hits the source's index page and pulls every
// `backup<date>` link out of it. We intentionally do a regex scrape on
// the raw body instead of pulling in a full HTML parser — the upstream
// listings are simple Apache `<a href>` tables and a tiny parser keeps
// this package free of new deps.
func listFromHTMLIndex(ctx context.Context, baseURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", baseURL, resp.StatusCode)
	}

	// 4 MB is generous — a typical mirror index is < 100 KB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}

	seen := map[string]struct{}{}
	for _, m := range backupRE.FindAllStringSubmatch(string(body), -1) {
		name := "backup" + m[1]
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		// Strip the "backup" prefix and any dashes so YYYYMMDD compares lexically.
		a := normalizeDate(out[i])
		b := normalizeDate(out[j])
		return a > b
	})
	return out, nil
}

// generateDateList yields plausible nile backup names. Nile rotates
// daily for a month, then keeps the 10/20/30 of each month for ~6 months.
// This mirrors tron-docker's logic so users see the same set of options.
func generateDateList() []string {
	now := time.Now().UTC()
	out := make([]string, 0, 60)
	for i := 1; i < 180; i++ {
		d := now.AddDate(0, 0, -i)
		// Nile uses unhyphenated YYYYMMDD; we keep that format for
		// consistency with the URL builder.
		dateStr := d.Format("20060102")
		if i < 30 {
			out = append(out, "backup"+dateStr)
			continue
		}
		switch d.Day() {
		case 10, 20, 30:
			out = append(out, "backup"+dateStr)
		}
	}
	// Already newest-first by construction.
	return out
}

// normalizeDate removes the literal "backup" prefix and any hyphens so
// two date formats sort consistently.
func normalizeDate(name string) string {
	s := strings.TrimPrefix(name, "backup")
	s = strings.ReplaceAll(s, "-", "")
	return s
}
