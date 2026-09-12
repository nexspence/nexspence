package storage_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestNewAzureBlobStore_EmptyContainer_Error(t *testing.T) {
	_, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container")
}

func TestNewAzureBlobStore_IdentityWithoutAccount_Error(t *testing.T) {
	// No key, no connection string, no SAS, and no account name: the
	// constructor must refuse before touching any credential chain.
	_, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container: "nx",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_name")
}

func TestNewAzureBlobStore_BadConnectionString_Error(t *testing.T) {
	_, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:        "nx",
		ConnectionString: "not-a-connection-string",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection string")
}

// azureFake is a minimal Azure Blob endpoint speaking the real REST shapes
// (GET ?restype=container&comp=metadata, PUT/GET/HEAD/DELETE blob, GET
// ?restype=container&comp=list) — the wire formats are defined by the
// documented service protocol, not by this backend, so the fake cannot
// mirror a bug in the adapter back to it (the #347/#349 lesson).
type azureFake struct {
	t        *testing.T
	server   string
	blobs    map[string][]byte
	modTimes map[string]time.Time
	requests []string
}

func newAzureFake(t *testing.T) *azureFake {
	t.Helper()
	f := &azureFake{t: t, blobs: map[string][]byte{}, modTimes: map[string]time.Time{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.server = srv.URL
	return f
}

func (f *azureFake) serve(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.String())
	// Path = /<container>/<blobpath...>
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) < 2 {
		// Container-scoped request.
		switch {
		case r.URL.Query().Get("restype") == "container" && r.URL.Query().Get("comp") == "":
			w.WriteHeader(http.StatusOK)
		case r.URL.Query().Get("comp") == "metadata":
			w.WriteHeader(http.StatusOK)
		case r.URL.Query().Get("comp") == "list":
			f.list(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	blob := "/" + parts[1]
	switch r.Method {
	case http.MethodPut:
		var body []byte
		if r.Body != nil {
			buf := make([]byte, 0, r.ContentLength)
			b := make([]byte, 4096)
			for {
				n, err := r.Body.Read(b)
				body = append(body, b[:n]...)
				if err != nil || n == 0 {
					break
				}
			}
			_ = buf
		}
		f.blobs[blob] = body
		f.modTimes[blob] = time.Now().UTC()
		w.Header().Set("ETag", `"fake"`)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		data, ok := f.blobs[blob]
		if !ok {
			f.notFound(w)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Last-Modified", f.modTimes[blob].Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodHead:
		data, ok := f.blobs[blob]
		if !ok {
			w.Header().Set("x-ms-error-code", "BlobNotFound")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Last-Modified", f.modTimes[blob].Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if _, ok := f.blobs[blob]; !ok {
			f.notFound(w)
			return
		}
		delete(f.blobs, blob)
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *azureFake) notFound(w http.ResponseWriter) {
	w.Header().Set("x-ms-error-code", "BlobNotFound")
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><Error><Code>BlobNotFound</Code><Message>The specified blob does not exist.</Message></Error>`))
}

// list answers ?restype=container&comp=list with the documented XML shape.
func (f *azureFake) list(w http.ResponseWriter) {
	type props struct {
		ContentLength int64  `xml:"Content-Length"`
		LastModified  string `xml:"Last-Modified"`
	}
	type item struct {
		Name       string `xml:"Name"`
		Properties props  `xml:"Properties"`
	}
	type segment struct {
		BlobItems []item `xml:"Blob"`
	}
	type listing struct {
		XMLName xml.Name `xml:"EnumerationResults"`
		Blobs   segment  `xml:"Blobs"`
	}
	var out listing
	for k, v := range f.blobs {
		out.Blobs.BlobItems = append(out.Blobs.BlobItems, item{
			Name: strings.TrimPrefix(k, "/"),
			Properties: props{
				ContentLength: int64(len(v)),
				LastModified:  f.modTimes[k].Format(http.TimeFormat),
			},
		})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(out)
}

func (f *azureFake) store(t *testing.T) *storage.AzureBlobStore {
	t.Helper()
	ctx := context.Background()
	// SAS-token mode against the fake: the constructor's container probe
	// hits the fake's metadata handler, and blob URLs need no auth there.
	srv := f.server
	bs, err := storage.NewAzureBlobStore(ctx, storage.AzureOptions{
		Container:   "nx",
		SASToken:    "sv=2024-11-04&sig=fake",
		AccountName: "devstoreaccount1",
		Endpoint:    srv,
	})
	require.NoError(t, err)
	return bs
}

func TestAzure_Put_Get_Roundtrip(t *testing.T) {
	f := newAzureFake(t)
	bs := f.store(t)
	ctx := context.Background()

	payload := []byte("hello azure")
	require.NoError(t, bs.Put(ctx, "abcdef-key", strings.NewReader(string(payload)), int64(len(payload))))

	// Sharded like S3: ab/cd/abcdef-key under the container (the SDK
	// escapes the shard slashes; the service decodes them again).
	var sawPut bool
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PUT /nx/ab%2Fcd%2Fabcdef-key") {
			sawPut = true
		}
	}
	assert.True(t, sawPut, "no sharded PUT in %v", f.requests)

	rc, size, err := bs.Get(ctx, "abcdef-key")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 64)
	n, _ := rc.Read(buf)
	assert.Equal(t, payload, buf[:n])
	assert.Equal(t, int64(len(payload)), size)

	exists, err := bs.Exists(ctx, "abcdef-key")
	require.NoError(t, err)
	assert.True(t, exists)

	size, err = bs.Size(ctx, "abcdef-key")
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), size)

	require.NoError(t, bs.Delete(ctx, "abcdef-key"))
	exists, err = bs.Exists(ctx, "abcdef-key")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestAzure_Get_Missing_MapsToNotFound(t *testing.T) {
	f := newAzureFake(t)
	bs := f.store(t)
	_, _, err := bs.Get(context.Background(), "missing-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob not found")
}

func TestAzure_TransportError_RedactsSASAndPreservesErrorChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	bs, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:   "nx",
		AccountName: "devstoreaccount1",
		SASToken:    "sv=2024-11-04&sig=transport-secret",
		Endpoint:    srv.URL,
	})
	require.NoError(t, err)
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = bs.ListKeys(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "transport-secret")
	assert.NotContains(t, err.Error(), "sig=")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the sanitized error must preserve errors.Is to the transport cancellation")
}

func TestAzure_ListKeys_StripsShard_ExcludesAppendMeta(t *testing.T) {
	f := newAzureFake(t)
	bs := f.store(t)
	ctx := context.Background()

	write := func(key string, n int) {
		require.NoError(t, bs.Put(ctx, key, strings.NewReader(strings.Repeat("x", n)), int64(n)))
	}
	write("aabbccdd", 10)
	write("eeff0011", 20)
	// Bookkeeping side-blob written straight through the listing path:
	f.blobs["/ee/ff/aabbccdd.append-meta"] = []byte("{}")
	f.modTimes["/ee/ff/aabbccdd.append-meta"] = time.Now().UTC()

	keys, err := bs.ListKeys(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"aabbccdd", "eeff0011"}, keys)

	used, err := bs.UsedBytes(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 30, used, ".append-meta must not count toward quota")
}

func TestAzure_ConfigureLifecycle_RejectedAsAccountScoped(t *testing.T) {
	f := newAzureFake(t)
	bs := f.store(t)
	err := bs.ConfigureLifecycle(context.Background(), 30)
	require.ErrorIs(t, err, storage.ErrLifecycleUnsupported)
	assert.Contains(t, err.Error(), "account-scoped")
}

func TestAzure_Presign_SharedKey_URLCarriesSASQuery(t *testing.T) {
	// The constructor probes the container; signing itself is offline.
	// The endpoint must point at the fake so no test ever dials real Azure.
	f := newAzureFake(t)
	bs, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:   "nx",
		AccountName: "acct",
		AccountKey:  "a2V5LWZvci10ZXN0aW5nCg==", // base64 shape azblob expects
		Endpoint:    f.server,
	})
	require.NoError(t, err)

	url, err := bs.PresignGetURL(context.Background(), "aabbccdd", time.Hour)
	require.NoError(t, err)
	// The SDK escapes the shard slashes in the blob path; the service decodes them.
	assert.Contains(t, url, "/nx/aa%2Fbb%2Faabbccdd?")
	for _, q := range []string{"sig=", "se=", "sp=", "sv="} {
		assert.Contains(t, url, q, "SAS query parameter %s missing", q)
	}

	putURL, err := bs.PresignPutURL(context.Background(), "aabbccdd", time.Hour)
	require.NoError(t, err)
	assert.Contains(t, putURL, "sig=")
}

func TestAzure_Presign_SASTokenStore_Rejected(t *testing.T) {
	f := newAzureFake(t)
	bs := f.store(t)
	// A store built on someone else's SAS cannot sign further SAS tokens.
	_, err := bs.PresignGetURL(context.Background(), "aabbccdd", time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires an account key or Entra ID credential")
}
