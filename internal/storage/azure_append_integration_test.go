//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Append integration tests against Azurite, mirroring
// s3_append_integration_test.go: the chunked OCI push path over staged
// blocks, its HA guarantee, and its failure modes.

// chunkReader yields data in fixed read windows so AppendBlob sees the same
// read pattern a PATCHed HTTP body would.
func chunkReader(data []byte) io.Reader { return bytes.NewReader(data) }

func TestAzure_Append_MultiChunk_Finalize_Visible(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "appe1001"

	// Two chunks well above the 4 MiB staging floor force real blocks.
	chunk1 := bytes.Repeat([]byte("A"), 5*1024*1024)
	chunk2 := bytes.Repeat([]byte("B"), 4*1024*1024+1000) // crosses the floor again

	total, err := bs.AppendBlob(ctx, key, chunkReader(chunk1))
	require.NoError(t, err)
	assert.EqualValues(t, len(chunk1), total)

	total, err = bs.AppendBlob(ctx, key, chunkReader(chunk2))
	require.NoError(t, err)
	assert.EqualValues(t, len(chunk1)+len(chunk2), total)

	// Invisible until finalized.
	exists, err := bs.Exists(ctx, key)
	require.NoError(t, err)
	if exists {
		size, err := bs.Size(ctx, key)
		require.NoError(t, err)
		assert.Zero(t, size, "nothing may be visible at the key before FinalizeAppend")
	}

	staged, ok, err := bs.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.EqualValues(t, len(chunk1)+len(chunk2), staged)

	require.NoError(t, bs.FinalizeAppend(ctx, key))

	rc, size, err := bs.Get(ctx, key)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	require.NoError(t, err)
	assert.EqualValues(t, len(chunk1)+len(chunk2), size)
	assert.Equal(t, string(chunk1)+string(chunk2), string(got))
	require.NoError(t, bs.Delete(ctx, key))
}

func TestAzure_Append_Abort_LeavesNoBlob(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "abor2002"

	_, err := bs.AppendBlob(ctx, key, chunkReader(bytes.Repeat([]byte("Z"), 6*1024*1024)))
	require.NoError(t, err)
	require.NoError(t, bs.AbortAppend(ctx, key))

	exists, err := bs.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	_, ok, err := bs.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok, "no session may survive AbortAppend")
}

func TestAzure_Append_SurvivesStoreRecreation(t *testing.T) {
	// The HA guarantee: no in-process state. A second store instance — as
	// another replica would build it — continues a push the first started.
	bs1 := azuriteStore(t)
	ctx := context.Background()
	key := "hass3003"

	part1 := bytes.Repeat([]byte("C"), 5*1024*1024)
	_, err := bs1.AppendBlob(ctx, key, chunkReader(part1))
	require.NoError(t, err)

	bs2 := azuriteStore(t)
	part2 := bytes.Repeat([]byte("D"), 1024)
	total, err := bs2.AppendBlob(ctx, key, chunkReader(part2))
	require.NoError(t, err)
	assert.EqualValues(t, len(part1)+len(part2), total)

	require.NoError(t, bs2.FinalizeAppend(ctx, key))
	rc, size, err := bs1.Get(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.EqualValues(t, len(part1)+len(part2), size)
	assert.Equal(t, string(part1)+string(part2), string(got))
	require.NoError(t, bs1.Delete(ctx, key))
}

func TestAzure_Append_ExtendsExistingBlob(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "extd4004"

	old := bytes.Repeat([]byte("O"), 1000) // below the staging floor
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(old), int64(len(old))))
	_, err := bs.AppendBlob(ctx, key, chunkReader([]byte("more")))
	require.NoError(t, err)
	require.NoError(t, bs.FinalizeAppend(ctx, key))

	rc, size, err := bs.Get(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.EqualValues(t, 1004, size)
	assert.True(t, bytes.HasPrefix(got, old) && bytes.HasSuffix(got, []byte("more")),
		"append must extend, not replace, the existing bytes")
	require.NoError(t, bs.Delete(ctx, key))
}

func TestAzure_Truncate_ShrinksPendingTail(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "trnc5005"

	_, err := bs.AppendBlob(ctx, key, chunkReader([]byte("0123456789")))
	require.NoError(t, err)
	require.NoError(t, bs.TruncateBlob(ctx, key, 5))

	staged, ok, err := bs.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.EqualValues(t, 5, staged)

	require.NoError(t, bs.FinalizeAppend(ctx, key))
	rc, _, err := bs.Get(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "01234", string(got))
	require.NoError(t, bs.Delete(ctx, key))
}

func TestAzure_Truncate_PastStagedBlocks_Errors(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "trnf6006"

	_, err := bs.AppendBlob(ctx, key, chunkReader(bytes.Repeat([]byte("X"), 5*1024*1024)))
	require.NoError(t, err)
	// Below the already-staged block total: must fail loudly, not drop
	// staged data silently.
	err = bs.TruncateBlob(ctx, key, 1024)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be rolled back")
	require.NoError(t, bs.AbortAppend(ctx, key))
}

func TestAzure_Finalize_NoSessionIsIdempotent(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	require.NoError(t, bs.FinalizeAppend(ctx, "nofi7007"))
	require.NoError(t, bs.AbortAppend(ctx, "nofi7007"))
}

func TestAzure_Delete_AbortsInFlightAppend(t *testing.T) {
	// GC collecting an abandoned session must clear the bookkeeping too —
	// the side-blob is the only handle to the staged blocks' session.
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "dlgt8008"

	_, err := bs.AppendBlob(ctx, key, chunkReader([]byte("half a push")))
	require.NoError(t, err)
	require.NoError(t, bs.Delete(ctx, key))

	_, ok, err := bs.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok, "Delete must clear the append session")
}

// Appending to a blob that Put wrote as several blocks inherits the SDK's
// 64-byte block IDs. Azure requires one uniform ID length per blob, so the
// session's own IDs have to match — Azurite tolerates a mix, hence the
// explicit assertion on the committed list rather than just the bytes.
func TestAzure_Append_OntoMultiBlockBlob_UniformBlockIDs(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "amblk006"

	seed := bytes.Repeat([]byte("S"), 9*1024*1024) // several 4 MiB blocks
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(seed), int64(len(seed))))
	n, err := bs.AppendBlob(ctx, key, chunkReader([]byte("tail")))
	require.NoError(t, err)
	// The offset a resumed chunked push continues from: the seed counts.
	assert.EqualValues(t, len(seed)+4, n)
	require.NoError(t, bs.FinalizeAppend(ctx, key))

	rc, size, err := bs.Get(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.EqualValues(t, len(seed)+4, size)
	assert.True(t, bytes.HasPrefix(got, seed) && bytes.HasSuffix(got, []byte("tail")))

	widths := map[int]int{}
	for _, id := range committedBlockIDs(t, key) {
		raw, derr := base64.StdEncoding.DecodeString(id)
		require.NoError(t, derr)
		widths[len(raw)]++
	}
	assert.Len(t, widths, 1, "every committed block id must decode to the same length, got %v — real Azure answers 400 InvalidBlockList", widths)
	require.NoError(t, bs.Delete(ctx, key))
}

// A blob written in one Put Blob (a presigned upload, a server-side copy)
// has no block list to inherit. Its bytes must be re-staged in blocks, not
// pulled into memory — a layer can be gigabytes.
func TestAzure_Append_OntoSinglePutBlob_RestagesWithoutBuffering(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	key := "asngl007"

	seed := bytes.Repeat([]byte("P"), 9*1024*1024)
	putSingleBlob(t, key, seed)
	require.Empty(t, committedBlockIDs(t, key), "precondition: a single Put Blob leaves no block list")

	n, err := bs.AppendBlob(ctx, key, chunkReader([]byte("tail")))
	require.NoError(t, err)
	assert.EqualValues(t, len(seed)+4, n, "AppendedSize must account for the re-staged seed")
	require.NoError(t, bs.FinalizeAppend(ctx, key))

	rc, size, err := bs.Get(ctx, key)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.EqualValues(t, len(seed)+4, size)
	assert.True(t, bytes.HasPrefix(got, seed) && bytes.HasSuffix(got, []byte("tail")))
	// Re-staged in blocks: the pending tail alone could never hold 9 MiB.
	assert.Greater(t, len(committedBlockIDs(t, key)), 1, "seed must have been re-staged as blocks")
	require.NoError(t, bs.Delete(ctx, key))
}

// blockBlobClient talks to a key directly, bypassing the store, so a test can
// assert on Azure-side state the BlobStore interface does not expose.
func blockBlobClient(t *testing.T, key string) *blockblob.Client {
	t.Helper()
	cred, err := blob.NewSharedKeyCredential(azuriteAccount, azuriteKey)
	require.NoError(t, err)
	// The store shards keys as ab/cd/<key>; mirror it here.
	objKey := key[:2] + "/" + key[2:4] + "/" + key
	bc, err := blockblob.NewClientWithSharedKeyCredential(
		azuriteSvcURL+"/"+azuriteContainer+"/"+objKey, cred, nil)
	require.NoError(t, err)
	return bc
}

func committedBlockIDs(t *testing.T, key string) []string {
	t.Helper()
	list, err := blockBlobClient(t, key).GetBlockList(context.Background(), blockblob.BlockListTypeCommitted, nil)
	require.NoError(t, err)
	ids := make([]string, 0, len(list.CommittedBlocks))
	for _, b := range list.CommittedBlocks {
		if b != nil && b.Name != nil {
			ids = append(ids, *b.Name)
		}
	}
	return ids
}

// putSingleBlob uploads data with one Put Blob — what a presigned upload URL
// or an Azure server-side copy leaves behind: no committed block list.
func putSingleBlob(t *testing.T, key string, data []byte) {
	t.Helper()
	_, err := blockBlobClient(t, key).Upload(context.Background(), readSeekNopCloser{bytes.NewReader(data)}, nil)
	require.NoError(t, err)
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }
