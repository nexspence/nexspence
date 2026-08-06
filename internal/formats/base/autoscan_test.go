package base_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// recordingScanner captures the component ids the storage layer asks to scan.
type recordingScanner struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordingScanner) TriggerAsync(componentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, componentID)
}

func (r *recordingScanner) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

// Every upload is queued for a scan, so a new artifact cannot sit unscanned
// waiting for someone to remember to ask.
func TestStoreArtifact_QueuesStoredComponentForScanning(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	d, _, comps, _ := deps(repo)
	scanner := &recordingScanner{}
	d.Scanner = scanner

	content := `{"name":"lodash"}`
	result, err := base.StoreArtifact(context.Background(), d,
		"npm-hosted", "/lodash/-/lodash-4.17.20.tgz", "application/octet-stream",
		base.Coords{Name: "lodash", Version: "4.17.20"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	comp, err := comps.Get(context.Background(), result.Asset.ComponentID)
	require.NoError(t, err)
	assert.Equal(t, []string{comp.ID}, scanner.seen())
}

// Auto-scan is optional: a Deps without a scanner stores artifacts exactly as
// before.
func TestStoreArtifact_NilScannerIsAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, blobStore, _, _ := deps(repo)
	require.Nil(t, d.Scanner)

	_, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/notes.txt", "text/plain",
		base.Coords{Name: "notes.txt"},
		strings.NewReader("data"), 4)

	require.NoError(t, err)
	assert.True(t, blobStore.Has(base.BlobKey("raw-hosted", "/notes.txt")))
}
