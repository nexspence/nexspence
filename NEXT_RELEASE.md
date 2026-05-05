## Bug Fixes

### Vulnerability scan fails after container restart (`MANIFEST_UNKNOWN`)

Docker image manifests were stored at a path derived from the DB blob store config
(`./data/blobs/default`), which resolves to `/app/data/blobs/default` inside the
container — outside the persistent volume mounted at `/data/blobs`. After a restart
the directory was empty, so Trivy received `MANIFEST_UNKNOWN` from the registry.

**Fix:** `syncBlobStorePaths()` runs at startup and updates every local blob store's
`path` in the DB to `filepath.Join(basePath, storeName)`, matching the configured
`NEXSPENCE_STORAGE_LOCAL_BASE_PATH`. Hosted repos now survive restarts without
requiring a re-push.

### Proxy Docker repos always fail scan with "image not found"

Two separate issues caused scans to fail on proxy-type Docker repositories even when
the image was visibly cached:

1. `repoproxy.ServeGET` wrote cached blobs to `d.BlobStore` (base path `/data/blobs`)
   but `RegisterStoredBlob` recorded the "default" blob store ID (path
   `/data/blobs/default`). On read, `PhysicalStore` looked in the wrong directory.
   Fixed by using `base.ResolveBlobStore` so the write location and the DB record
   always point to the same physical store.

2. When the cached blob file was missing (e.g. after a path change), `ServeGET`
   returned 502 instead of falling through to an upstream fetch. Fixed: a blob-get
   error is now treated as a cache miss — the asset is refetched from upstream and
   re-cached at the correct location.
