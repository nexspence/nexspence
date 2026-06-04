package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestNewS3BlobStore_EmptyBucket_Error(t *testing.T) {
	_, err := storage.NewS3BlobStore(context.Background(), storage.S3Options{
		Bucket: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}

func TestNewS3BlobStore_ValidOptions_NoNetwork(t *testing.T) {
	// The SDK constructor does NOT make a network call — it only builds the client.
	// Connectivity is validated by the caller (blobstores handler test-connection endpoint).
	bs, err := storage.NewS3BlobStore(context.Background(), storage.S3Options{
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		Endpoint:        "http://127.0.0.1:19000", // unreachable, but constructor succeeds
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
	})
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewS3BlobStore_WithCredentials_NoNetwork(t *testing.T) {
	bs, err := storage.NewS3BlobStore(context.Background(), storage.S3Options{
		Bucket:          "bucket",
		Region:          "eu-west-1",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
	})
	require.NoError(t, err)
	require.NotNil(t, bs)
}
