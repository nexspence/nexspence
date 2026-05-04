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

// OSVVuln is a single vulnerability from the OSV.dev API.
type OSVVuln struct {
	ID       string
	Summary  string
	Severity string // "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN"
}

// OSVClient queries the OSV.dev vulnerability database.
type OSVClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

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
		ID      string   `json:"id"`
		Aliases []string `json:"aliases"`
		Summary string   `json:"summary"`
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv.dev returned %d", resp.StatusCode)
	}

	var result osvQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	out := make([]OSVVuln, 0, len(result.Vulns))
	for _, v := range result.Vulns {
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
