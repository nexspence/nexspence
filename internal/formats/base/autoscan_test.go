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

// Not every write goes through StoreArtifact: a proxy repository caching an
// upstream artifact registers the blob directly. Those are the artifacts most
// worth scanning — a compromised upstream release arrives this way — so the
// trigger has to sit on the path they share, not on the upload path alone.
func TestRegisterStoredBlob_QueuesComponentForScanning(t *testing.T) {
	repo := testutil.SimpleRepo("npm-proxy", "npm")
	d, _, _, _ := deps(repo)
	scanner := &recordingScanner{}
	d.Scanner = scanner

	asset, err := base.RegisterStoredBlob(context.Background(), d, repo,
		"/lodash/-/lodash-4.17.20.tgz", "application/octet-stream",
		base.Coords{Name: "lodash", Version: "4.17.20"},
		"blobkey", "sha256sum", "sha1sum", "md5sum", 42, "", "")
	require.NoError(t, err)

	assert.Equal(t, []string{asset.ComponentID}, scanner.seen())
}

// An OCI push registers every layer as its own digest-versioned component,
// before the manifest. Queuing those would fill a bounded queue with entries
// that get discarded at the far end — and the manifest, the only one worth
// scanning, arrives last and is the one that gets dropped.
func TestStoreArtifact_DoesNotQueueDigestVersionedComponents(t *testing.T) {
	repo := testutil.SimpleRepo("docker-hosted", "docker")
	d, _, _, _ := deps(repo)
	scanner := &recordingScanner{}
	d.Scanner = scanner

	digest := "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"
	_, err := base.StoreArtifact(context.Background(), d,
		"docker-hosted", "/v2/myapp/blobs/"+digest, "application/octet-stream",
		base.Coords{Name: "myapp", Version: digest},
		strings.NewReader("layer bytes"), 11)
	require.NoError(t, err)

	assert.Empty(t, scanner.seen(), "a layer blob must not reach the scan queue")
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
