package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A stored scan row is announced to WithScanCompleted — how an auto-promotion
// waiting for a scan learns it has one (#542); a row that failed to store is not.
func TestScanService_ScanCompletedHook(t *testing.T) {
	comp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	_ = comps.Create(context.Background(), comp)
	rows := testutil.NewScanResultRepo()

	var got []string
	svc := service.NewScanService(comps, "").WithScanResults(rows).
		WithScanCompleted(func(_ context.Context, id string) { got = append(got, id) })
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	if _, err := svc.Scan(context.Background(), comp.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != comp.ID {
		t.Fatalf("hook calls = %v, want one for %s", got, comp.ID)
	}

	rows.InsertErr = errors.New("insert failed")
	_ = captureLog(t, func() { _, _ = svc.Scan(context.Background(), comp.ID, "") })
	if len(got) != 1 {
		t.Fatalf("hook fired for a row that was not stored: %v", got)
	}
}
