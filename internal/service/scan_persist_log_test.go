package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// captureLog redirects the standard logger for the duration of fn and returns
// what was written to it.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// osvServerWithOneVuln serves a single HIGH finding for any query.
func osvServerWithOneVuln(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vulns": []map[string]any{
				{"id": "CVE-2021-9999", "summary": "Prototype pollution",
					"database_specific": map[string]any{"severity": "HIGH"}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A scan that cannot be written back leaves the dashboard showing stale data for
// the component. The scan still succeeds for the caller — but it must not be the
// only place the failure is ever visible.
func TestScanService_LogsWhenResultNotPersistedToComponent(t *testing.T) {
	comp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)
	comps.UpdateExtraErr = errors.New("connection reset by peer")

	svc := service.NewScanService(comps, "")
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	var result *domain.ScanResult
	var err error
	out := captureLog(t, func() {
		result, err = svc.Scan(context.Background(), comp.ID, "")
	})

	if err != nil {
		t.Fatalf("a failed write must not fail the scan: %v", err)
	}
	if result.Summary.High != 1 {
		t.Errorf("the caller still gets the findings, got %#v", result.Summary)
	}
	for _, want := range []string{comp.ID, "osv", "connection reset by peer"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not mention %q", out, want)
		}
	}
}

// Same defect on the other write: the scan_results history row the org-wide
// dashboard aggregates from.
func TestScanService_LogsWhenHistoryRowNotInserted(t *testing.T) {
	comp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	scanRepo := testutil.NewScanResultRepo()
	scanRepo.InsertErr = errors.New("deadlock detected")

	svc := service.NewScanService(comps, "").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	var err error
	out := captureLog(t, func() {
		_, err = svc.Scan(context.Background(), comp.ID, "")
	})

	if err != nil {
		t.Fatalf("a failed write must not fail the scan: %v", err)
	}
	for _, want := range []string{comp.ID, "osv", "deadlock detected"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not mention %q", out, want)
		}
	}
}
