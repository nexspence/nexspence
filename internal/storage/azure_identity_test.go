package storage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestPhysicalStoreIdentity_Azure_UsesEffectiveEndpoint(t *testing.T) {
	base := storage.BlobStoreDescriptor{Type: "azure", Config: map[string]any{"container": "blobs", "account_name": "actual"}}
	want := storage.PhysicalStoreIdentity(base)
	cs := "DefaultEndpointsProtocol=https;AccountName=actual;AccountKey=YQ==;EndpointSuffix=core.windows.net"
	for _, cfg := range []map[string]any{
		{"container": "blobs", "connection_string": cs},
		{"container": "blobs", "connection_string": "DefaultEndpointsProtocol=https;AccountName=actual;AccountKey=invalid-key;EndpointSuffix=core.windows.net"},
		{"container": "blobs", "connection_string": cs, "account_name": "ignored", "endpoint": "https://ignored.example"},
		{"container": "blobs", "endpoint": "https://actual.blob.core.windows.net", "sas_token": "sig=fake"},
		{"container": "blobs", "connection_string": "BlobEndpoint=https://actual.blob.core.windows.net;SharedAccessSignature=sv=2021-08-06&sig=fake"},
	} {
		require.Equal(t, want, storage.PhysicalStoreIdentity(storage.BlobStoreDescriptor{Type: "azure", Config: cfg}))
	}
	for _, cs := range []string{
		"DefaultEndpointsProtocol=https;AccountName=other;AccountKey=YQ==;EndpointSuffix=core.windows.net",
		"DefaultEndpointsProtocol=https;AccountName=actual;AccountKey=YQ==;EndpointSuffix=core.usgovcloudapi.net",
		"BlobEndpoint=https://actual.blob.core.windows.net/other;SharedAccessSignature=sig=fake",
	} {
		require.NotEqual(t, want, storage.PhysicalStoreIdentity(storage.BlobStoreDescriptor{Type: "azure", Config: map[string]any{"container": "blobs", "connection_string": cs}}))
	}
	require.Empty(t, storage.PhysicalStoreIdentity(storage.BlobStoreDescriptor{Type: "azure", Config: map[string]any{"connection_string": "invalid"}}))
}
