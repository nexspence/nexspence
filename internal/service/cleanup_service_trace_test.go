package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A cleanup run's log lines carry the run's own trace id (#321): the root span
// RunAll opens is the same trace every line inside the run correlates to.
func TestCleanupService_RunLogsCarryTraceID(t *testing.T) {
	prev := otel.GetTracerProvider()
	defer otel.SetTracerProvider(prev)
	exp := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample())))

	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core).Sugar()

	repos := testutil.NewRepoRepo(testutil.SimpleRepo("raw-host", "raw"))
	policies := testutil.NewCleanupPolicyRepo()
	require.NoError(t, policies.Create(context.Background(), &domain.CleanupPolicy{
		Name: "dry", Enabled: true, DryRun: true, Format: "*",
		Scope:    domain.CleanupScope{RepositoryName: "raw-host"},
		Criteria: map[string]any{"artifactAgeDays": 1},
	}))

	svc := service.NewCleanupService(policies, repos, testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), log)

	require.NoError(t, svc.RunAll(context.Background()))

	spans := exp.GetSpans()
	var rootTrace string
	for _, sp := range spans {
		if sp.Name == "cleanup.run_all" {
			rootTrace = sp.SpanContext.TraceID().String()
		}
	}
	require.NotEmpty(t, rootTrace, "RunAll must open its root span")

	require.NotZero(t, logs.Len(), "the dry run should have logged")
	for _, entry := range logs.All() {
		assert.Equal(t, rootTrace, entry.ContextMap()["trace_id"],
			"log %q must carry the run's trace id", entry.Message)
	}
}
