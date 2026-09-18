package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// AzureBlobStore stores blobs as Azure Block Blobs in one blob container.
// Keys are stored with the same two-level prefix shard as the S3 backend:
// ab/cd/<key>. One blob store equals one container; repository-level
// separation is done by registering several stores, never by prefixes.
type AzureBlobStore struct {
	container *container.Client
	// sharedKey is the storage account key when the client authenticates
	// with one (explicit or embedded in a connection string) — the
	// credential for signing a service SAS offline.
	sharedKey *blob.SharedKeyCredential
	// tokenCred is the Entra ID credential when the store was built on
	// DefaultAzureCredential; presigning then goes through a user
	// delegation SAS instead.
	tokenCred azcore.TokenCredential
	// clientOptions carries the transport tweaks (SkipTLSVerify) so the
	// lazily-built service client for user delegation matches the
	// container client.
	clientOptions service.ClientOptions
	serviceURL    string
	containerName string
	// udcMu guards the cached user delegation credential. Minting one is a
	// network round trip against Entra ID, and it is valid for days, so
	// presigning reuses it instead of paying per URL.
	udcMu     sync.Mutex
	udc       *service.UserDelegationCredential
	udcExpiry time.Time
}

// Compile-time proof that Azure stores expose every capability the S3
// backend does; callers keep using type assertions against the interfaces.
var (
	_ BlobStore           = (*AzureBlobStore)(nil)
	_ PresignableStore    = (*AzureBlobStore)(nil)
	_ AppendableBlobStore = (*AzureBlobStore)(nil)
)

// AzureOptions configures the Azure Blob Storage-backed blob store.
type AzureOptions struct {
	// Container is the blob container to write into. It must already
	// exist — Nexspence never creates or deletes containers.
	Container string
	// AccountName is the storage account, e.g. "mystorage" (service URL
	// https://mystorage.blob.core.windows.net) or the last path segment
	// target when Endpoint points at a custom/emulator service URL.
	AccountName string
	// AccountKey is the shared (account) key. It enables service-SAS
	// presigning.
	AccountKey string
	// ConnectionString is the Azure "DefaultEndpointsProtocol=..." string.
	// Takes precedence over AccountName/AccountKey/SASToken.
	ConnectionString string
	// SASToken authenticates via a pre-generated SAS appended to the
	// container URL. Signing further SAS URLs from a SAS is impossible,
	// so presigning is unavailable in this mode.
	SASToken string
	// Endpoint overrides the public-cloud service URL, e.g.
	// http://127.0.0.1:10000/devstoreaccount1 for Azurite or a sovereign
	// cloud endpoint.
	Endpoint string
	// SkipTLSVerify turns off certificate verification against Endpoint —
	// the same operator opt-in the S3 backend offers for private CAs (#403).
	SkipTLSVerify bool
}

// NewAzureBlobStore creates an AzureBlobStore and validates that the target
// container exists. Unlike the S3 constructor this does make one network
// call: a wrong container name or credential otherwise only surfaces on the
// first blob write, long after the store was saved.
func NewAzureBlobStore(ctx context.Context, opts AzureOptions) (*AzureBlobStore, error) {
	if opts.Container == "" {
		return nil, fmt.Errorf("azure: container name is required")
	}

	copts := container.ClientOptions{}
	var sopts service.ClientOptions
	if opts.SkipTLSVerify {
		tr := &http.Transport{}
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = def.Clone()
		}
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // per-store operator opt-in for a private CA, mirroring the S3 backend (#403)
		copts.Transport = &transportClient{client: &http.Client{Transport: tr}}
		sopts.Transport = copts.Transport
	}

	s := &AzureBlobStore{containerName: opts.Container}

	switch {
	case opts.ConnectionString != "":
		cc, err := container.NewClientFromConnectionString(opts.ConnectionString, opts.Container, &copts)
		if err != nil {
			return nil, fmt.Errorf("azure: parse connection string: %w", redactAzureError(err))
		}
		s.container = cc
		parts := parseConnectionString(opts.ConnectionString)
		s.serviceURL = strings.TrimRight(parts["blobendpoint"], "/")
		// A connection string that carries an AccountKey authenticates
		// with a shared key internally, so service-SAS presigning works —
		// rebuild the credential for offline signing.
		if acct, skey := parts["accountname"], parts["accountkey"]; acct != "" && skey != "" {
			sk, kerr := blob.NewSharedKeyCredential(acct, skey)
			if kerr != nil {
				return nil, fmt.Errorf("azure: connection string account key: %w", redactAzureError(kerr))
			}
			s.sharedKey = sk
		}
	case opts.AccountName != "" && opts.AccountKey != "":
		key, err := blob.NewSharedKeyCredential(opts.AccountName, opts.AccountKey)
		if err != nil {
			return nil, fmt.Errorf("azure: account key: %w", redactAzureError(err))
		}
		svcURL := azureServiceURL(opts.Endpoint, opts.AccountName)
		cc, err := container.NewClientWithSharedKeyCredential(svcURL+"/"+opts.Container, key, &copts)
		if err != nil {
			return nil, fmt.Errorf("azure: container client: %w", redactAzureError(err))
		}
		s.container = cc
		s.sharedKey = key
		s.serviceURL = svcURL
	case opts.SASToken != "":
		svcURL := azureServiceURL(opts.Endpoint, opts.AccountName)
		token := strings.TrimPrefix(opts.SASToken, "?")
		cc, err := container.NewClientWithNoCredential(svcURL+"/"+opts.Container+"?"+token, &copts)
		if err != nil {
			return nil, fmt.Errorf("azure: container client: %w", redactAzureError(err))
		}
		s.container = cc
		s.serviceURL = svcURL
	default:
		// Entra ID: managed identity, az CLI login, service principal, ...
		if opts.AccountName == "" {
			return nil, fmt.Errorf("azure: account_name is required when no account key, connection string or SAS token is configured")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure: default credential: %w", redactAzureError(err))
		}
		svcURL := azureServiceURL(opts.Endpoint, opts.AccountName)
		cc, err := container.NewClient(svcURL+"/"+opts.Container, cred, &copts)
		if err != nil {
			return nil, fmt.Errorf("azure: container client: %w", redactAzureError(err))
		}
		s.container = cc
		s.tokenCred = cred
		s.serviceURL = svcURL
	}
	s.clientOptions = sopts

	// Connectivity check: the container must exist, or every later write
	// fails with a confusing per-blob error.
	if _, err := s.container.GetProperties(ctx, nil); err != nil {
		return nil, fmt.Errorf("azure: container %s: %w", opts.Container, redactAzureError(err))
	}
	return s, nil
}

// azureServiceURL returns the blob service endpoint for an account: the
// operator-provided override if set, else the public-cloud URL.
func azureServiceURL(endpoint, accountName string) string {
	if endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	return "https://" + accountName + ".blob.core.windows.net"
}

// parseConnectionString splits an Azure connection string into its
// key=value parts (keys lower-cased).
func parseConnectionString(cs string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(cs, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

// transportClient adapts an *http.Client to the azcore transport interface.
// The URL it fetches is the operator-configured service endpoint baked into
// the client at construction, never a per-request target — the Do call is
// not an SSRF surface (gosec cannot see that).
type transportClient struct{ client *http.Client }

//nolint:gosec // target URL is operator config fixed at construction, not request input
func (t *transportClient) Do(req *http.Request) (*http.Response, error) {
	return t.client.Do(req)
}

// redactedAzureError preserves the original error chain for errors.Is/errors.As
// while keeping Azure request credentials out of the error text consumed by
// HTTP handlers, logs and traces. The Azure SDK's url.Error includes the full
// request URL for transport failures; SAS credentials are part of that URL's
// query string.
type redactedAzureError struct{ cause error }

func (e *redactedAzureError) Error() string {
	if e == nil || e.cause == nil {
		return "<nil>"
	}
	return redactAzureURLs(e.cause.Error())
}

func (e *redactedAzureError) Unwrap() error { return e.cause }

var azureURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// redactAzureError makes an Azure SDK error safe to expose while retaining the
// original error below the wrapper for errors.Is/errors.As.
func redactAzureError(err error) error {
	if err == nil {
		return nil
	}
	return &redactedAzureError{cause: err}
}

func redactAzureURLs(message string) string {
	return azureURLPattern.ReplaceAllStringFunc(message, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
}

// objectKey shards the blob key into a two-level prefix to avoid hot-spotting,
// exactly like the S3 backend.
func (s *AzureBlobStore) objectKey(key string) string {
	if len(key) >= 4 {
		return key[:2] + "/" + key[2:4] + "/" + key
	}
	return key
}

// Put uploads a blob as a block blob. Large uploads are split into blocks
// and streamed concurrently — nothing is buffered whole in memory.
func (s *AzureBlobStore) Put(ctx context.Context, key string, r io.Reader, declaredSize int64) (err error) {
	ctx, span := blobSpan(ctx, "blobstore.azure.put", key, declaredSize)
	defer func() { finishSpan(span, err) }()
	bc := s.container.NewBlockBlobClient(s.objectKey(key))
	_, err = bc.UploadStream(ctx, r, &blockblob.UploadStreamOptions{
		BlockSize:   azureBlockSize(declaredSize),
		Concurrency: 5,
	})
	if err != nil {
		return fmt.Errorf("azure put %s: %w", key, redactAzureError(err))
	}
	return nil
}

// azureBlockSize picks a block size for a stream of declaredSize bytes.
// Azure allows 50,000 blocks of up to 4000 MiB each; staying near the low
// end of that budget with a floor of 4 MiB keeps moderate blobs parallel
// and huge ones inside the block limit. An unknown size uses the default.
func azureBlockSize(declaredSize int64) int64 {
	const (
		minBlock int64 = 4 * 1024 * 1024
		maxBlock int64 = 256 * 1024 * 1024
	)
	if declaredSize <= 0 {
		return 0 // SDK default; the size is not trusted when unknown
	}
	if declaredSize <= minBlock {
		return minBlock
	}
	size := declaredSize / 49000
	if size < minBlock {
		return minBlock
	}
	if size > maxBlock {
		return maxBlock
	}
	return size
}

// Get downloads the blob for key and returns its body and size.
func (s *AzureBlobStore) Get(ctx context.Context, key string) (rc io.ReadCloser, size int64, err error) {
	ctx, span := blobSpan(ctx, "blobstore.azure.get", key, -1)
	defer func() { finishSpan(span, err) }()
	res, err := s.container.NewBlobClient(s.objectKey(key)).DownloadStream(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, 0, fmt.Errorf("blob not found: %s", key)
		}
		return nil, 0, fmt.Errorf("azure get %s: %w", key, redactAzureError(err))
	}
	if res.ContentLength != nil {
		size = *res.ContentLength
	}
	return res.Body, size, nil
}

// Delete removes the blob for key; a missing blob is not an error.
func (s *AzureBlobStore) Delete(ctx context.Context, key string) (err error) {
	ctx, span := blobSpan(ctx, "blobstore.azure.delete", key, -1)
	defer func() { finishSpan(span, err) }()
	// An unfinished append has staged blocks that no listing shows;
	// dropping the key without clearing the session first would strand
	// the bookkeeping side-blob, whose only consumer is gone. Best
	// effort: a failed cleanup must not block the delete.
	_ = s.AbortAppend(ctx, key)
	_, err = s.container.NewBlobClient(s.objectKey(key)).Delete(ctx, nil)
	if err != nil && !isAzureNotFound(err) {
		return fmt.Errorf("azure delete %s: %w", key, redactAzureError(err))
	}
	return nil
}

// Exists reports whether a blob is stored for key.
func (s *AzureBlobStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.container.NewBlobClient(s.objectKey(key)).GetProperties(ctx, nil)
	if err == nil {
		return true, nil
	}
	if isAzureNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("azure properties %s: %w", key, redactAzureError(err))
}

// Size returns the byte size of the blob for key.
func (s *AzureBlobStore) Size(ctx context.Context, key string) (int64, error) {
	res, err := s.container.NewBlobClient(s.objectKey(key)).GetProperties(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return 0, fmt.Errorf("blob not found: %s", key)
		}
		return 0, fmt.Errorf("azure properties %s: %w", key, redactAzureError(err))
	}
	if res.ContentLength != nil {
		return *res.ContentLength, nil
	}
	return 0, nil
}

// ListKeys returns all blob keys in the container by stripping the
// two-level shard prefix. Append bookkeeping is hidden, exactly as in the
// S3 backend.
func (s *AzureBlobStore) ListKeys(ctx context.Context) ([]string, error) {
	var keys []string
	pager := s.container.NewListBlobsFlatPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return keys, fmt.Errorf("azure list keys: %w", redactAzureError(err))
		}
		if page.Segment == nil {
			continue
		}
		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil || isAppendMetaObject(*item.Name) {
				continue
			}
			keys = append(keys, blobKeyFromObjectKey(*item.Name))
		}
	}
	return keys, nil
}

// ListEntries returns every blob's key, size and last-modified time,
// correlating .append-meta activity so GC never age-gates on a session's
// placeholder blob — see the matching comment in s3.go.
func (s *AzureBlobStore) ListEntries(ctx context.Context) ([]BlobEntry, error) {
	var entries []BlobEntry
	metaModTimes := make(map[string]time.Time)
	pager := s.container.NewListBlobsFlatPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return entries, fmt.Errorf("azure list entries: %w", redactAzureError(err))
		}
		if page.Segment == nil {
			continue
		}
		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil {
				continue
			}
			if isAppendMetaObject(*item.Name) {
				if item.Properties == nil || item.Properties.LastModified == nil {
					continue
				}
				key := blobKeyFromObjectKey(strings.TrimSuffix(*item.Name, appendMetaSuffix))
				if mt := *item.Properties.LastModified; mt.After(metaModTimes[key]) {
					metaModTimes[key] = mt
				}
				continue
			}
			var size int64
			var mt time.Time
			if item.Properties != nil {
				if item.Properties.ContentLength != nil {
					size = *item.Properties.ContentLength
				}
				if item.Properties.LastModified != nil {
					mt = *item.Properties.LastModified
				}
			}
			entries = append(entries, BlobEntry{Key: blobKeyFromObjectKey(*item.Name), Size: size, ModTime: mt})
		}
	}
	for i := range entries {
		if mt, ok := metaModTimes[entries[i].Key]; ok && mt.After(entries[i].ModTime) {
			entries[i].ModTime = mt
		}
	}
	return entries, nil
}

// UsedBytes sums the size of all blobs in the container. Append
// bookkeeping is excluded — it is not stored blob content, and counting it
// would push quota usage over what the store actually holds.
// This iterates every blob and may be slow on large containers; the S3
// backend carries the same caveat.
func (s *AzureBlobStore) UsedBytes(ctx context.Context) (int64, error) {
	var total int64
	pager := s.container.NewListBlobsFlatPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return total, fmt.Errorf("azure list: %w", redactAzureError(err))
		}
		if page.Segment == nil {
			continue
		}
		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil || isAppendMetaObject(*item.Name) {
				continue
			}
			if item.Properties != nil && item.Properties.ContentLength != nil {
				total += *item.Properties.ContentLength
			}
		}
	}
	return total, nil
}

// PresignGetURL returns a time-limited read-only SAS URL for the blob.
func (s *AzureBlobStore) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	perms := sas.BlobPermissions{Read: true}
	return s.presign(ctx, key, ttl, perms)
}

// PresignPutURL returns a time-limited upload SAS URL for the blob.
func (s *AzureBlobStore) PresignPutURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	perms := sas.BlobPermissions{Read: true, Create: true, Write: true}
	return s.presign(ctx, key, ttl, perms)
}

// sasVersion pins the sv parameter of the SAS tokens we mint. It lags the
// SDK's current service version on purpose: SAS tokens are validated
// against sv by the service, and storage emulators (Azurite) and older
// stacks reject versions they have never seen — the SDK's GetSASURL would
// embed its own newest version and break there. Real Azure accepts any sv
// it has ever shipped, so an older pin is compatible everywhere.
const sasVersion = "2021-08-06"

func (s *AzureBlobStore) presign(ctx context.Context, key string, ttl time.Duration, perms sas.BlobPermissions) (string, error) {
	expiry := time.Now().Add(ttl)
	objKey := s.objectKey(key)
	if s.sharedKey != nil {
		qps, err := sas.BlobSignatureValues{
			Version:       sasVersion,
			ContainerName: s.containerName,
			BlobName:      objKey,
			Permissions:   perms.String(),
			ExpiryTime:    expiry.UTC(),
		}.SignWithSharedKey(s.sharedKey)
		if err != nil {
			return "", fmt.Errorf("azure presign %s: %w", key, redactAzureError(err))
		}
		return s.container.NewBlobClient(objKey).URL() + "?" + qps.Encode(), nil
	}
	if s.tokenCred == nil {
		// A store created on a SAS token cannot sign further SAS tokens;
		// the caller's only path back is the normal streaming API.
		return "", fmt.Errorf("azure presign %s: requires an account key or Entra ID credential, not a SAS token store", key)
	}
	// User delegation SAS: sign offline with a delegation key minted by
	// Entra ID. The key's validity window must contain the SAS's.
	now := time.Now().UTC()
	udc, err := s.delegationCredential(ctx, expiry.UTC())
	if err != nil {
		return "", fmt.Errorf("azure presign %s: %w", key, redactAzureError(err))
	}
	qps, err := sas.BlobSignatureValues{
		Version:       sasVersion,
		ContainerName: s.containerName,
		BlobName:      objKey,
		Permissions:   perms.String(),
		StartTime:     now.Add(-15 * time.Minute),
		ExpiryTime:    expiry.UTC(),
	}.SignWithUserDelegation(udc)
	if err != nil {
		return "", fmt.Errorf("azure presign %s: sign: %w", key, redactAzureError(err))
	}
	return s.container.NewBlobClient(objKey).URL() + "?" + qps.Encode(), nil
}

// delegationCredential returns a user delegation credential whose validity
// window covers sasExpiry, reusing the cached one while it lasts.
//
// A fresh key costs a round trip to Entra ID and Azure throttles the call, so
// minting one per presigned URL made every download link pay for it. Keys are
// valid for up to 7 days; this asks for a day at a time (or longer when a SAS
// outlives that) and re-mints shortly before expiry, so a cached key is never
// handed out with less life left than the SAS it signs.
func (s *AzureBlobStore) delegationCredential(ctx context.Context, sasExpiry time.Time) (*service.UserDelegationCredential, error) {
	const (
		// azureDelegationKeyTTL is how far ahead a key is minted.
		azureDelegationKeyTTL = 24 * time.Hour
		// azureDelegationKeyMax is the service's hard ceiling.
		azureDelegationKeyMax = 7 * 24 * time.Hour
		// skew tolerates clocks drifting on both ends of the signature.
		skew = 15 * time.Minute
	)

	s.udcMu.Lock()
	defer s.udcMu.Unlock()

	now := time.Now().UTC()
	if s.udc != nil && s.udcExpiry.After(sasExpiry) && now.Add(5*time.Minute).Before(s.udcExpiry) {
		return s.udc, nil
	}

	keyExpiry := now.Add(azureDelegationKeyTTL)
	if wanted := sasExpiry.Add(skew); wanted.After(keyExpiry) {
		keyExpiry = wanted
	}
	if ceiling := now.Add(azureDelegationKeyMax); keyExpiry.After(ceiling) {
		keyExpiry = ceiling
		if sasExpiry.After(keyExpiry) {
			return nil, fmt.Errorf("user delegation key: a SAS expiring %s outlives the 7-day maximum "+
				"of the delegation key signing it; use a shorter ttl or an account key", sasExpiry.Format(time.RFC3339))
		}
	}

	svc, err := service.NewClient(s.serviceURL, s.tokenCred, &s.clientOptions)
	if err != nil {
		return nil, fmt.Errorf("service client: %w", redactAzureError(err))
	}
	start := now.Add(-skew).Format(time.RFC3339)
	exp := keyExpiry.Format(time.RFC3339)
	udc, err := svc.GetUserDelegationCredential(ctx, service.KeyInfo{Start: &start, Expiry: &exp}, nil)
	if err != nil {
		return nil, fmt.Errorf("user delegation key: %w", redactAzureError(err))
	}
	s.udc, s.udcExpiry = udc, keyExpiry
	return udc, nil
}

// ConfigureLifecycle refuses to run: Azure lifecycle policies are scoped to
// the whole storage account, not to a container, so a rule set here would
// expire foreign containers on shared accounts — real data loss. Operators
// manage expiry via account policies filtered to this container instead.
func (s *AzureBlobStore) ConfigureLifecycle(_ context.Context, _ int32) error {
	return fmt.Errorf("azure: lifecycle rules are account-scoped; manage expiry via Azure account policies filtered to container %s: %w",
		s.containerName, ErrLifecycleUnsupported)
}

// isAzureNotFound reports whether err is a 404 from the blob service.
// ContainerNotFound counts too: the test-connection probe must answer
// "not found" for the sentinel key, not an opaque error.
func isAzureNotFound(err error) bool {
	return bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound)
}
