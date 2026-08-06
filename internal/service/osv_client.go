package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const osvDefaultBaseURL = "https://api.osv.dev"

// SeverityMalicious marks a package that a malicious-package report names — a
// compromised release, not a package with a vulnerability in it.
//
// It is not a CVSS level and does not come from one. OSV.dev indexes OpenSSF's
// malicious-packages dataset under `MAL-…` ids, and those reports never carry a
// `database_specific.severity`, so without a tier of their own they landed in
// "Unknown" next to packages the scanner simply could not classify.
const SeverityMalicious = "MALICIOUS"

const osvMaliciousIDPrefix = "MAL-"

// OSVVuln is a single vulnerability from the OSV.dev API.
type OSVVuln struct {
	ID       string
	Summary  string
	Severity string // "MALICIOUS" | "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN"
}

// OSVClient queries the OSV.dev vulnerability database.
type OSVClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewOSVClient returns an OSVClient pointing at the default OSV.dev API endpoint.
func NewOSVClient() *OSVClient {
	return &OSVClient{
		BaseURL:    osvDefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type osvQueryRequest struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version string `json:"version"`
}

type osvQueryResponse struct {
	Vulns []struct {
		ID               string   `json:"id"`
		Aliases          []string `json:"aliases"`
		Summary          string   `json:"summary"`
		DatabaseSpecific struct {
			Severity string `json:"severity"`
		} `json:"database_specific"`
	} `json:"vulns"`
}

// Query returns all known vulnerabilities for the given package name, version, and ecosystem.
func (c *OSVClient) Query(ctx context.Context, name, version, ecosystem string) ([]OSVVuln, error) {
	var req osvQueryRequest
	req.Package.Name = name
	req.Package.Ecosystem = ecosystem
	req.Version = version

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("osv: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv.dev returned %d", resp.StatusCode)
	}

	var result osvQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	out := make([]OSVVuln, 0, len(result.Vulns))
	for _, v := range result.Vulns {
		// A malicious-package report outranks everything else the entry says.
		// Its severity is forced — a MAL- report that happens to carry a CVSS
		// score is still malware first — and its id is preferred over the CVE
		// alias the display logic would otherwise pick, because a CVE number in
		// place of a MAL- id hides exactly the fact worth seeing.
		if malID := maliciousID(v.ID, v.Aliases); malID != "" {
			out = append(out, OSVVuln{ID: malID, Summary: v.Summary, Severity: SeverityMalicious})
			continue
		}

		id := v.ID
		for _, alias := range v.Aliases {
			if strings.HasPrefix(alias, "CVE-") {
				id = alias
				break
			}
		}
		sev := strings.ToUpper(v.DatabaseSpecific.Severity)
		if sev == "" {
			sev = "UNKNOWN"
		}
		out = append(out, OSVVuln{ID: id, Summary: v.Summary, Severity: sev})
	}
	return out, nil
}

// maliciousID returns the MAL- identifier of an OSV entry — its own id or the
// first alias carrying one — or "" if the entry is not a malicious-package report.
func maliciousID(id string, aliases []string) string {
	if strings.HasPrefix(id, osvMaliciousIDPrefix) {
		return id
	}
	for _, alias := range aliases {
		if strings.HasPrefix(alias, osvMaliciousIDPrefix) {
			return alias
		}
	}
	return ""
}

// FormatToEcosystem maps Nexspence format names to OSV.dev ecosystem strings.
// Returns "" if the format is not supported.
func FormatToEcosystem(format string) string {
	switch strings.ToLower(format) {
	case "maven":
		return "Maven"
	case "npm":
		return "npm"
	case "pypi":
		return "PyPI"
	case "cargo":
		return "crates.io"
	default:
		return ""
	}
}
