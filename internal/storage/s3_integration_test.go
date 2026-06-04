//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── MinIO container (shared across all S3 integration tests) ─────────────────

var (
	minioOnce     sync.Once
	minioEndpoint string
	minioErr      error
)

const (
	minioBucket = "nexspence-test"
	minioUser   = "minioadmin"
	minioPass   = "minioadmin"
)

func minioPool(t *testing.T) *storage.S3BlobStore {
	t.Helper()
	minioOnce.Do(startMinio)
	if minioErr != nil {
		t.Fatalf("s3 integration: start minio: %v", minioErr)
	}

	ctx := context.Background()
	bs, err := storage.NewS3BlobStore(ctx, storage.S3Options{
		Bucket:          minioBucket,
		Region:          "us-east-1",
		Endpoint:        minioEndpoint,
		AccessKeyID:     minioUser,
		SecretAccessKey: minioPass,
		ForcePathStyle:  true,
	})
	if err != nil {
		t.Fatalf("s3 integration: new store: %v", err)
	}
	return bs
}

func startMinio() {
	pool, err := dockertest.NewPool("")
	if err != nil {
		minioErr = fmt.Errorf("connect to docker: %w", err)
		return
	}
	if err := pool.Client.Ping(); err != nil {
		minioErr = fmt.Errorf("docker ping: %w", err)
		return
	}

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "minio/minio",
		Tag:        "latest",
		Cmd:        []string{"server", "/data"},
		Env: []string{
			"MINIO_ROOT_USER=" + minioUser,
			"MINIO_ROOT_PASSWORD=" + minioPass,
		},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		minioErr = fmt.Errorf("start minio container: %w", err)
		return
	}
	_ = resource.Expire(300)

	hostPort := resource.GetHostPort("9000/tcp")
	endpoint := "http://" + hostPort
	minioEndpoint = endpoint

	// Wait for MinIO to accept connections and create the test bucket.
	pool.MaxWait = 60 * time.Second
	if err := pool.Retry(func() error {
		return createBucket(context.Background(), endpoint)
	}); err != nil {
		minioErr = fmt.Errorf("minio ready check: %w", err)
	}
}

func createBucket(ctx context.Context, endpoint string) error {
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(minioUser, minioPass, "")),
	)
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(minioBucket),
	})
	return err
}

// ── S3BlobStore integration tests ────────────────────────────────────────────

func TestS3BlobStore_PutGet_Roundtrip(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "aabbccdd11223344"
	data := []byte("hello s3 blob")

	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	rc, size, err := bs.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, data, got)
	assert.EqualValues(t, len(data), size)
}

func TestS3BlobStore_Put_ShortKey(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "ab"
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader([]byte("short")), 5))
	rc, _, err := bs.Get(ctx, key)
	require.NoError(t, err)
	rc.Close()
}

func TestS3BlobStore_Get_NotFound(t *testing.T) {
	bs := minioPool(t)
	_, _, err := bs.Get(context.Background(), "nonexistent99887766")
	require.Error(t, err)
}

func TestS3BlobStore_Delete_Existing(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "deadbeef11223344"
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader([]byte("del")), 3))
	require.NoError(t, bs.Delete(ctx, key))
	exists, err := bs.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestS3BlobStore_Delete_NotFound_NoError(t *testing.T) {
	bs := minioPool(t)
	require.NoError(t, bs.Delete(context.Background(), "missing99112233"))
}

func TestS3BlobStore_Exists(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "cafebabe11223344"

	ok, err := bs.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, bs.Put(ctx, key, bytes.NewReader([]byte("y")), 1))
	ok, err = bs.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestS3BlobStore_Size(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "1234abcd11223344"
	data := []byte("sizecheck")
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(data), int64(len(data))))
	sz, err := bs.Size(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, len(data), sz)
}

func TestS3BlobStore_Size_NotFound(t *testing.T) {
	bs := minioPool(t)
	_, err := bs.Size(context.Background(), "ghost99887766aa")
	require.Error(t, err)
}

func TestS3BlobStore_ListKeys(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	// Use unique prefix to avoid interference between parallel test runs.
	keys := []string{"ff00aa1122334455", "ee11bb2233445566"}
	for _, k := range keys {
		require.NoError(t, bs.Put(ctx, k, bytes.NewReader([]byte("v")), 1))
	}
	all, err := bs.ListKeys(ctx)
	require.NoError(t, err)
	for _, k := range keys {
		assert.Contains(t, all, k)
	}
}

func TestS3BlobStore_UsedBytes(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "aaaa1111bbbb2222", bytes.NewReader([]byte("123")), 3))
	n, err := bs.UsedBytes(ctx)
	require.NoError(t, err)
	assert.Positive(t, n)
}

func TestS3BlobStore_PresignGetURL(t *testing.T) {
	bs := minioPool(t)
	ctx := context.Background()
	key := "presign11223344aa"
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader([]byte("p")), 1))

	presignable, ok := interface{}(bs).(storage.PresignableStore)
	require.True(t, ok, "S3BlobStore must implement PresignableStore")

	url, err := presignable.PresignGetURL(ctx, key, time.Minute)
	require.NoError(t, err)
	assert.Contains(t, url, "presign")
}

func TestS3BlobStore_PresignPutURL(t *testing.T) {
	bs := minioPool(t)
	presignable, ok := interface{}(bs).(storage.PresignableStore)
	require.True(t, ok, "S3BlobStore must implement PresignableStore")

	url, err := presignable.PresignPutURL(context.Background(), "newblob11223344", time.Minute)
	require.NoError(t, err)
	assert.NotEmpty(t, url)
}

func TestS3BlobStore_ConfigureLifecycle_SetAndRemove(t *testing.T) {
	bs := minioPool(t)
	presignable, ok := interface{}(bs).(storage.PresignableStore)
	require.True(t, ok)

	ctx := context.Background()
	// Set lifecycle rule (MinIO supports this).
	err := presignable.ConfigureLifecycle(ctx, 30)
	require.NoError(t, err)

	// Remove lifecycle rules.
	err = presignable.ConfigureLifecycle(ctx, 0)
	require.NoError(t, err)
}
