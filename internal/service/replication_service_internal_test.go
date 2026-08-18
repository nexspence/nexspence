package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// One run per rule per process: detached from the request context (#254),
// repeated POSTs to .../run would otherwise pile up concurrent runs of the
// same rule pushing the same assets.
func TestReplicationService_RunRule_RefusesConcurrentRun(t *testing.T) {
	repRepo := testutil.NewReplicationRepo()
	svc := NewReplicationService(repRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, zap.NewNop().Sugar())

	rule := &domain.ReplicationRule{Name: "solo", SourceRepo: "src", TargetURL: "http://127.0.0.1:1/", TargetRepo: "dst"}
	require.NoError(t, repRepo.CreateRule(context.Background(), rule))

	// Occupy the guard the way an in-flight run does.
	svc.running.Store(rule.ID, struct{}{})
	defer svc.running.Delete(rule.ID)

	err := svc.RunRule(context.Background(), rule.ID)
	require.ErrorContains(t, err, "already running")
}
