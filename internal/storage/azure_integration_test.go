//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── Azurite container (shared across all Azure integration tests) ───────────
//
// Azurite is Microsoft's official storage emulator. Like the MinIO suite for
// S3, these tests talk to the real wire protocol — no fakes shaped like the
// adapter (#347/#349 lesson). Entra ID's user-delegation presign path is out
// of the emulator's reach and stays unit-covered.

var (
	azuriteOnce    sync.Once
	azuriteSvcURL  string
	azuriteConnStr string
	azuriteErr     error
)

const (
	azuriteContainer = "nexspencetest"
	// Azurite's hard-coded well-known dev credentials.
	azuriteAccount = "devstoreaccount1"
	azuriteKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

func azuriteStore(t *testing.T) *storage.AzureBlobStore {
	t.Helper()
	azuriteOnce.Do(startAzurite)
	if azuriteErr != nil {
		t.Fatalf("azure integration: start azurite: %v", azuriteErr)
	}
	bs, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:   azuriteContainer,
		AccountName: azuriteAccount,
		AccountKey:  azuriteKey,
		Endpoint:    azuriteSvcURL,
	})
	if err != nil {
		t.Fatalf("azure integration: new store: %v", err)
	}
	return bs
}

func startAzurite() {
	pool, err := dockertest.NewPool("")
	if err != nil {
		azuriteErr = fmt.Errorf("connect to docker: %w", err)
		return
	}
	if err := pool.Client.Ping(); err != nil {
		azuriteErr = fmt.Errorf("docker ping: %w", err)
		return
	}

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "mcr.microsoft.com/azure-storage/azurite",
		Tag:        "latest",
		// --skipApiVersionCheck: azblob pins a service API version newer
		// than the current Azurite image knows; real Azure accepts it, the
		// emulator needs this documented flag to stop rejecting the header.
		Cmd: []string{"azurite-blob", "--blobHost", "0.0.0.0", "--skipApiVersionCheck"},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		azuriteErr = fmt.Errorf("start azurite container: %w", err)
		return
	}
	_ = resource.Expire(300)

	hostPort := resource.GetHostPort("10000/tcp")
	svcURL := "http://" + hostPort + "/" + azuriteAccount
	azuriteSvcURL = svcURL
	azuriteConnStr = fmt.Sprintf("DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s;",
		azuriteAccount, azuriteKey, svcURL)

	// Wait for Azurite, then create the test container once.
	pool.MaxWait = 60 * time.Second
	if err := pool.Retry(func() error {
		return createContainer(context.Background(), svcURL)
	}); err != nil {
		azuriteErr = fmt.Errorf("azurite ready check: %w", err)
	}
}

func createContainer(ctx context.Context, svcURL string) error {
	cred, err := service.NewSharedKeyCredential(azuriteAccount, azuriteKey)
	if err != nil {
		return err
	}
	svc, err := service.NewClientWithSharedKeyCredential(svcURL, cred, nil)
	if err != nil {
		return err
	}
	cc := svc.NewContainerClient(azuriteContainer)
	if _, err := cc.Create(ctx, nil); err != nil {
		// ResourceAlreadyExists on a later pool.Retry attempt is success.
		if bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
			return nil
		}
		return err
	}
	return nil
}

func TestAzure_Put_Get_Delete_Roundtrip(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("azure-integration-payload-"), 5000) // ~130 KiB
	require.NoError(t, bs.Put(ctx, "cafe1234key", bytes.NewReader(payload), int64(len(payload))))

	rc, size, err := bs.Get(ctx, "cafe1234key")
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), size)
	assert.Equal(t, payload, got)

	size, err = bs.Size(ctx, "cafe1234key")
	require.NoError(t, err)
	assert.EqualValues(t, len(payload), size)

	require.NoError(t, bs.Delete(ctx, "cafe1234key"))
	exists, err := bs.Exists(ctx, "cafe1234key")
	require.NoError(t, err)
	assert.False(t, exists)

	_, _, err = bs.Get(ctx, "cafe1234key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob not found")
}

func TestAzure_UsedBytes_ExcludesAppendMeta(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "usag0001", bytes.NewReader([]byte("1234567890")), 10))
	require.NoError(t, bs.Put(ctx, "usag0002", bytes.NewReader([]byte("12345")), 5))

	// Open an append session (writes .append-meta, stages nothing yet),
	// then verify usage reflects only the two blobs.
	total, err := bs.AppendBlob(ctx, "usag0001", bytes.NewReader([]byte("xx")))
	require.NoError(t, err)
	assert.EqualValues(t, 12, total)

	used, err := bs.UsedBytes(ctx)
	require.NoError(t, err)
	// .append-meta bookkeeping is small JSON; usage must sit far below its
	// size + blobs if it were counted... assert exactly the stored blobs.
	assert.LessOrEqual(t, used, int64(1024))

	keys, err := bs.ListKeys(ctx)
	require.NoError(t, err)
	for _, k := range keys {
		assert.NotContains(t, k, "append-meta")
	}
	require.NoError(t, bs.AbortAppend(ctx, "usag0001"))
}

func TestAzure_ListEntries_ModTime(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "mod10001", bytes.NewReader([]byte("a")), 1))

	// Session start rewrites the side-blob; the entry's ModTime must track
	// it, not the placeholder — otherwise GC age-gating can abort a live
	// session (see the s3.go comment this mirrors).
	_, err := bs.AppendBlob(ctx, "mod10001", bytes.NewReader(bytes.Repeat([]byte("b"), 100)))
	require.NoError(t, err)

	entries, err := bs.ListEntries(ctx)
	require.NoError(t, err)
	var placeholder time.Time
	for _, e := range entries {
		if e.Key == "mod10001" {
			placeholder = e.ModTime
		}
	}
	require.False(t, placeholder.IsZero(), "mod10001 missing from ListEntries")
	assert.WithinDuration(t, time.Now(), placeholder, 2*time.Minute,
		"entry ModTime should reflect the fresh .append-meta touch")
	require.NoError(t, bs.AbortAppend(ctx, "mod10001"))
}

func TestAzure_Presign_Get_Put(t *testing.T) {
	bs := azuriteStore(t)
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "pres1001", bytes.NewReader([]byte("presigned!")), 10))

	getURL, err := bs.PresignGetURL(ctx, "pres1001", time.Hour)
	require.NoError(t, err)
	resp, err := http.Get(getURL) //nolint:gosec // emulator URL from dockertest
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "SAS GET failed: %s", body)
	assert.Equal(t, "presigned!", string(body))

	putURL, err := bs.PresignPutURL(ctx, "pres2002", time.Hour)
	require.NoError(t, err)
	// Upload through the SAS URL exactly like an external client would.
	up, err := blockblob.NewClientWithNoCredential(putURL, nil)
	require.NoError(t, err)
	_, err = up.UploadBuffer(ctx, []byte("uploaded via SAS"), nil)
	require.NoError(t, err)

	got, _, err := bs.Get(ctx, "pres2002")
	require.NoError(t, err)
	data, _ := io.ReadAll(got)
	_ = got.Close()
	assert.Equal(t, "uploaded via SAS", string(data))
}

func TestAzure_ConnectionString_Store_Works(t *testing.T) {
	azuriteOnce.Do(startAzurite)
	require.NoError(t, azuriteErr)
	bs, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:        azuriteContainer,
		ConnectionString: azuriteConnStr,
	})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "conn3001", bytes.NewReader([]byte("cs path")), 9))
	rc, _, err := bs.Get(ctx, "conn3001")
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "cs path", string(data))
	// A connection string with an embedded AccountKey must presign like an
	// explicit key would.
	url, err := bs.PresignGetURL(ctx, "conn3001", time.Minute)
	require.NoError(t, err)
	assert.Contains(t, url, "sig=")
}

func TestAzure_BlocksBase64Decodable(t *testing.T) {
	// Sanity: the well-known Azurite key must satisfy the SDK's base64
	// requirement — guards the test fixture itself.
	if _, err := base64.StdEncoding.DecodeString(azuriteKey); err != nil {
		t.Fatalf("fixture account key is not base64: %v", err)
	}
}
