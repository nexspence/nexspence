package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/audit"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/db"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/safego"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

func main() {
	root := &cobra.Command{
		Use:   "nexspence",
		Short: "Nexspence — free universal artifact repository manager",
	}

	root.AddCommand(cmdServe(), cmdMigrate())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func cmdServe() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Nexspence HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			log := logger.New(cfg.Log.Level, cfg.Log.Format)
			log.Info("starting nexspence", "version", Version, "addr", cfg.HTTP.Addr)

			// Install any server-wide outbound proxy default for upstream fetches.
			// Per-repository proxy_config overrides these; when unset, env
			// HTTP_PROXY/HTTPS_PROXY/NO_PROXY are honored by the guarded client.
			if p := cfg.Proxy; p.HTTPProxy != "" || p.HTTPSProxy != "" || p.SOCKS5Proxy != "" || p.NoProxy != "" {
				repoproxy.SetGlobalProxy(p.HTTPProxy, p.HTTPSProxy, p.SOCKS5Proxy, p.NoProxy, p.Username, p.Password)
				log.Info("outbound proxy configured for upstream fetches",
					"http", p.HTTPProxy != "", "https", p.HTTPSProxy != "", "socks5", p.SOCKS5Proxy != "")
			}
			if err := checkStartupSecurity(cfg, log); err != nil {
				return err
			}

			// Auto-migrate on every startup so the schema is always up-to-date.
			log.Info("running database migrations...")
			if err := db.Migrate(cfg.Database.DSN, "up"); err != nil {
				return fmt.Errorf("migrations failed: %w", err)
			}
			log.Info("migrations OK")

			// Tracing has to be installed before anything that hangs spans off
			// the global provider: the router's otelgin middleware, the pool's
			// query tracer, blob-store and background-job spans.
			shutdownTracing, err := tracing.Init(cmd.Context(), tracing.Config{
				Enabled:      cfg.Tracing.Enabled,
				OTLPEndpoint: cfg.Tracing.OTLPEndpoint,
				OTLPProtocol: cfg.Tracing.OTLPProtocol,
				OTLPInsecure: cfg.Tracing.OTLPInsecure,
				SampleRatio:  cfg.Tracing.SampleRatio,
				ServiceName:  cfg.Tracing.ServiceName,
				Environment:  cfg.Tracing.Environment,
			}, Version)
			if err != nil {
				return err
			}
			defer func() { _ = shutdownTracing(context.Background()) }()
			var queryTracers []pgx.QueryTracer
			if cfg.Tracing.Enabled {
				log.Info("tracing enabled", "endpoint", cfg.Tracing.OTLPEndpoint,
					"protocol", cfg.Tracing.OTLPProtocol, "sample_ratio", cfg.Tracing.SampleRatio)
				// Span names are "query SELECT" etc. — since otelpgx 0.12 that
				// trimming is the default; the whole multi-line SQL statement
				// in the span name (#302) is now the opt-in WithFullSQLInSpanName.
				queryTracers = append(queryTracers, otelpgx.NewTracer())
			}

			pool, err := db.Connect(cmd.Context(), cfg.Database.DSN, queryTracers...)
			if err != nil {
				return err
			}
			defer pool.Close()
			log.Info("database connected", "host", dbHost(cfg.Database.DSN))

			// Storage
			switch cfg.Storage.DefaultType {
			case "s3":
				log.Info("storage", "type", "s3", "bucket", cfg.Storage.S3.Bucket, "endpoint", cfg.Storage.S3.Endpoint)
				if cfg.Storage.S3.SkipTLSVerify {
					log.Warn("storage.s3.skip_tls_verify is enabled — the S3 endpoint's certificate is NOT verified; use only with a private CA you cannot install into the trust store")
				}
			case "azure":
				log.Info("storage", "type", "azure", "container", cfg.Storage.Azure.Container, "account", cfg.Storage.Azure.AccountName, "endpoint", cfg.Storage.Azure.Endpoint)
				if cfg.Storage.Azure.SkipTLSVerify {
					log.Warn("storage.azure.skip_tls_verify is enabled — the Azure endpoint's certificate is NOT verified; use only with a private CA you cannot install into the trust store")
				}
			default:
				log.Info("storage", "type", "local", "path", cfg.Storage.Local.BasePath)
			}

			// LDAP
			if cfg.LDAP.Enabled {
				log.Info("ldap enabled", "host", cfg.LDAP.Host, "port", cfg.LDAP.Port, "use_tls", cfg.LDAP.UseTLS || cfg.LDAP.Port == 636, "insecure_skip_verify", cfg.LDAP.InsecureSkipVerify, "admin_group", cfg.LDAP.AdminGroup)
				if cfg.LDAP.InsecureSkipVerify {
					log.Warn("LDAP insecure_skip_verify is enabled — TLS certificate validation is OFF; use only with self-signed certs in development")
				}
				if ldapSvc := auth.NewLDAPService(cfg.LDAP); ldapSvc != nil {
					if err := ldapSvc.TestConnection(cmd.Context()); err != nil {
						log.Warn("ldap connection test FAILED", "err", err)
					} else {
						log.Info("ldap connection OK")
					}
				}
			} else {
				log.Info("ldap disabled")
			}

			// OIDC — startup discovery log. NewRouter will rebuild the service
			// at router construction; this is diagnostic-only.
			if cfg.OIDC.Enabled {
				log.Info("oidc enabled",
					"display", cfg.OIDC.DisplayName,
					"issuer", cfg.OIDC.Issuer,
					"provisioning", cfg.OIDC.Provisioning,
				)
				if _, err := auth.NewOIDCService(cmd.Context(), cfg.OIDC); err != nil {
					log.Warn("oidc discovery test FAILED", "err", err)
				} else {
					log.Info("oidc discovery OK")
				}
			} else {
				log.Info("oidc disabled")
			}

			// Audit retention — pre-create future partitions, drop expired,
			// observe row count. Synchronous first tick guarantees the
			// current month's partition exists before we accept traffic.
			rotator := audit.NewRotator(audit.NewPgPartitionStore(pool), cfg.Audit, log)
			rotator.RunOnce(cmd.Context())
			safego.Go(log, "audit-rotator", func() { rotator.Run(cmd.Context()) })
			log.Info("audit rotator started",
				"retention_days", cfg.Audit.RetentionDays,
				"soft_cap", cfg.Audit.SoftCap,
				"rotation_interval", cfg.Audit.RotationInterval.String(),
				"lookahead_months", cfg.Audit.LookaheadMonths,
			)

			if err := bootstrapAdmin(cmd.Context(), pool, cfg, log); err != nil {
				log.Error("bootstrap admin failed", "err", err)
				// Non-fatal — server still starts
			}

			if err := syncBlobStorePaths(cmd.Context(), pool, cfg, log); err != nil {
				log.Warn("blob store path sync failed", "err", err)
				// Non-fatal — server still starts
			}

			if err := syncS3BlobStores(cmd.Context(), pool, cfg, log); err != nil {
				log.Warn("s3 blob store sync failed", "err", err)
				// Non-fatal — server still starts
			}

			if err := syncAzureBlobStores(cmd.Context(), pool, cfg, log); err != nil {
				return fmt.Errorf("azure blob store sync failed: %w", err)
			}

			// Seed Prometheus gauges from DB on startup.
			{
				var artifacts, bytes, downloads int64
				_ = pool.QueryRow(cmd.Context(),
					`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
				).Scan(&artifacts, &bytes, &downloads)
				metrics.UpdateGauges(artifacts, bytes, downloads)
				log.Info("metrics gauges seeded", "artifacts", artifacts, "bytes", bytes)
			}

			// Start background metrics sampler — stops on context cancellation.
			samplerCtx, cancelSampler := context.WithCancel(cmd.Context())
			defer cancelSampler()
			metrics.StartSampler(samplerCtx, log, pool)

			// Lifetime of everything the router runs in the background. Kept
			// separate from the HTTP server's shutdown context so the two can be
			// wound down at the same time rather than one after the other.
			bgCtx, cancelBg := context.WithCancel(cmd.Context())
			defer cancelBg()

			router := api.NewRouter(bgCtx, cfg, pool, log, Version)

			srv := &http.Server{
				Addr:              cfg.HTTP.Addr,
				Handler:           router,
				ReadTimeout:       time.Duration(cfg.HTTP.ReadTimeoutSec) * time.Second,
				WriteTimeout:      time.Duration(cfg.HTTP.WriteTimeoutSec) * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       120 * time.Second,
			}
			if cfg.HTTP.TLS.Enabled {
				srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}

			// Start server in goroutine
			go func() {
				if cfg.HTTP.TLS.Enabled {
					if err := srv.ListenAndServeTLS(cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile); !errors.Is(err, http.ErrServerClosed) {
						log.Error("https server error", "err", err)
						os.Exit(1)
					}
				} else {
					if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
						log.Error("http server error", "err", err)
						os.Exit(1)
					}
				}
			}()

			// Graceful shutdown
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit

			log.Info("shutting down...")
			// Before Shutdown, not after it: the background jobs then wind down
			// while the server drains its in-flight requests, instead of being
			// left running until the deferred cancel fires on the way out.
			cancelBg()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return srv.Shutdown(ctx)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "Path to config file")
	return cmd
}

func cmdMigrate() *cobra.Command {
	var cfgPath string
	var direction string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			return db.Migrate(cfg.Database.DSN, direction)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "Path to config file")
	cmd.Flags().StringVarP(&direction, "direction", "d", "up", "Migration direction: up | down | status")
	return cmd
}

// syncBlobStorePaths ensures every local blob store in DB has an absolute "path"
// derived from cfg.Storage.Local.BasePath. The migration seed uses relative paths
// (e.g. "./data/blobs/default") which resolve to the wrong location when the app
// runs in Docker (WORKDIR=/app, volume mounted at /data/blobs). This sync runs on
// every startup so the DB always reflects the path the app was configured with.
func syncBlobStorePaths(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	if cfg.Storage.DefaultType != "" && cfg.Storage.DefaultType != "local" {
		return nil
	}
	basePath := cfg.Storage.Local.BasePath
	if basePath == "" {
		basePath = "./data/blobs"
	}

	blobRepo := postgres.NewBlobStoreRepo(pool)
	stores, err := blobRepo.List(ctx)
	if err != nil {
		return err
	}
	for i := range stores {
		bs := &stores[i]
		if bs.Type != "local" {
			continue
		}
		expectedPath := filepath.Join(basePath, bs.Name)
		if bs.Config == nil {
			bs.Config = map[string]any{}
		}
		currentPath, _ := bs.Config["path"].(string)
		if currentPath == expectedPath {
			continue
		}
		bs.Config["path"] = expectedPath
		if updateErr := blobRepo.Update(ctx, bs); updateErr != nil {
			log.Warn("blob store path sync failed", "name", bs.Name, "err", updateErr)
		} else {
			log.Info("blob store path synced", "name", bs.Name, "old", currentPath, "new", expectedPath)
		}
	}
	return nil
}

// seedBlobStores are the blob stores the initial migration seeds as local
// ("default", "docker"). "default" is the fallback store resolveBlobStoreRef
// uses for every repository without an explicit blobStoreId; "docker" is the
// store docker repositories may be assigned. Both are reconciled to s3 or
// azure when storage.default_type is set to that backend, so no local store
// silently captures writes.
var seedBlobStores = []string{"default", "docker"}

// desiredS3Config maps the config-file S3 settings onto the config-key names the
// storage registry expects when instantiating an s3 BlobStore. Note the key
// rename: the registry reads access_key/secret_key (see newFromDescriptor in
// internal/storage/registry.go), NOT the config file's access_key_id/secret_access_key.
func desiredS3Config(s3 config.S3Config) map[string]any {
	return map[string]any{
		"bucket":           s3.Bucket,
		"region":           s3.Region,
		"endpoint":         s3.Endpoint,
		"access_key":       s3.AccessKeyID,
		"secret_key":       s3.SecretAccessKey,
		"force_path_style": s3.ForcePathStyle,
		"skip_tls_verify":  s3.SkipTLSVerify,
	}
}

// syncS3BlobStores reconciles the configured S3 settings into the seed blob
// stores when storage.default_type=s3. Without it (issue #81) the seed migration
// leaves "default"/"docker" as local stores, so repositories with no explicit
// blobStoreId resolve to local disk even though the operator configured S3 —
// only the health display and the d.BlobStore fallback reflect S3. Mirrors
// syncBlobStorePaths: runs on every startup so the DB always reflects the
// configured storage backend.
func syncS3BlobStores(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	if cfg.Storage.DefaultType != "s3" {
		return nil
	}
	return reconcileS3BlobStores(ctx, postgres.NewBlobStoreRepo(pool), cfg.Storage.S3, nil, log)
}

// reconcileS3BlobStores upserts the seedBlobStores to type=s3 with s3, creating
// any that are missing. Idempotent: stores already matching the desired s3
// config are left untouched. When reg is non-nil, updated stores are
// invalidated so a cached local instance isn't reused (reg is nil on startup —
// the Registry is built after this runs, so it reads the reconciled rows fresh).
func reconcileS3BlobStores(ctx context.Context, blobRepo repository.BlobStoreRepo, s3 config.S3Config, reg *storage.Registry, log logger.Logger) error {
	desired := desiredS3Config(s3)
	for _, name := range seedBlobStores {
		bs, err := blobRepo.Get(ctx, name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		if bs == nil {
			if createErr := blobRepo.Create(ctx, &domain.BlobStore{Name: name, Type: "s3", Config: desired}); createErr != nil {
				return createErr
			}
			log.Info("s3 blob store created", "name", name, "bucket", s3.Bucket, "endpoint", s3.Endpoint)
			continue
		}
		if bs.Type == "s3" && s3ConfigEqual(bs.Config, desired) {
			continue // already reconciled
		}
		bs.Type = "s3"
		bs.Config = desired
		if updateErr := blobRepo.Update(ctx, bs); updateErr != nil {
			return updateErr
		}
		if reg != nil && bs.ID != "" {
			reg.Invalidate(bs.ID)
		}
		log.Info("s3 blob store reconciled", "name", name, "bucket", s3.Bucket, "endpoint", s3.Endpoint)
	}
	return nil
}

// desiredAzureConfig maps the config-file Azure settings onto the keys the
// storage registry expects when instantiating an azure BlobStore. Unlike S3
// there is no rename: the registry reads the same names as the config file.
func desiredAzureConfig(az config.AzureConfig) map[string]any {
	return map[string]any{
		"container":         az.Container,
		"account_name":      az.AccountName,
		"account_key":       az.AccountKey,
		"connection_string": az.ConnectionString,
		"sas_token":         az.SASToken,
		"endpoint":          az.Endpoint,
		"skip_tls_verify":   az.SkipTLSVerify,
	}
}

// syncAzureBlobStores reconciles the configured Azure settings into the seed
// blob stores when storage.default_type=azure. Same trap as issue #81 for S3:
// without it the seed migration leaves "default"/"docker" as local stores.
func syncAzureBlobStores(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	if cfg.Storage.DefaultType != "azure" {
		return nil
	}
	basePath := cfg.Storage.Local.BasePath
	if basePath == "" {
		basePath = "./data/blobs"
	}
	return reconcileAzureBlobStoresWithReferences(ctx, postgres.NewBlobStoreRepo(pool), azureBlobStoreReferences{
		repositories: postgres.NewRepositoryRepo(pool),
		assets:       postgres.NewAssetRepo(pool),
	}, basePath, cfg.Storage.Azure, nil, log)
}

// azureBlobStoreReferences is the read-only view used to prove that a seed
// store is still unused before changing its storage target.
type azureBlobStoreReferences struct {
	repositories repository.RepositoryRepo
	assets       repository.AssetRepo
}

func (r azureBlobStoreReferences) inUse(ctx context.Context, bs *domain.BlobStore) (bool, error) {
	if bs.UsedBytes > 0 {
		return true, nil
	}
	if r.repositories == nil && r.assets == nil {
		// The compatibility wrapper is used only by focused unit tests. Startup
		// always supplies both repositories and therefore never takes this path.
		return false, nil
	}
	if r.repositories != nil {
		linked, err := r.repositories.ListByBlobStoreID(ctx, bs.ID)
		if err != nil {
			return false, fmt.Errorf("list repositories for blob store %q: %w", bs.Name, err)
		}
		if len(linked) > 0 {
			return true, nil
		}
	}
	if r.assets != nil {
		refs, err := r.assets.ListAllBlobRefs(ctx)
		if err != nil {
			return false, fmt.Errorf("list assets for blob store %q: %w", bs.Name, err)
		}
		for _, ref := range refs {
			if ref.BlobStoreID == bs.ID || (ref.BlobStoreID == "" && bs.Name == "default") {
				return true, nil
			}
		}
	}
	return false, nil
}

// reconcileAzureBlobStores keeps the compact test-facing wrapper for the
// migration seed path. Startup uses the reference-aware variant below with
// the configured local base path.
func reconcileAzureBlobStores(ctx context.Context, blobRepo repository.BlobStoreRepo, az config.AzureConfig, reg *storage.Registry, log logger.Logger) error {
	return reconcileAzureBlobStoresWithReferences(ctx, blobRepo, azureBlobStoreReferences{}, "./data/blobs", az, reg, log)
}

// reconcileAzureBlobStoresWithReferences updates only untouched seed stores,
// or rotates credentials on an existing Azure store whose effective target is
// unchanged. Target changes for occupied or individually configured stores
// are rejected; moving their blobs belongs to BlobStoreMigrationService.
func reconcileAzureBlobStoresWithReferences(ctx context.Context, blobRepo repository.BlobStoreRepo, refs azureBlobStoreReferences, localBasePath string, az config.AzureConfig, reg *storage.Registry, log logger.Logger) error {
	desired := desiredAzureConfig(az)
	stores, err := blobRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("list blob stores: %w", err)
	}
	groupMembers := referencedBlobStoreIDs(stores)

	type pendingChange struct {
		store              *domain.BlobStore
		credentialRotation bool
	}
	var creates []*domain.BlobStore
	var updates []pendingChange
	var rejected []string

	for _, name := range seedBlobStores {
		bs, err := blobRepo.Get(ctx, name)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		if bs == nil {
			creates = append(creates, &domain.BlobStore{Name: name, Type: "azure", Config: cloneConfig(desired)})
			continue
		}
		if bs.Type == "azure" && reflect.DeepEqual(bs.Config, desired) {
			continue
		}
		if bs.Type == "azure" && azureConfigOnlyCredentialsDiffer(bs.Config, desired) {
			candidate := cloneBlobStore(bs)
			candidate.Config = cloneConfig(desired)
			updates = append(updates, pendingChange{store: candidate, credentialRotation: true})
			continue
		}
		if isUntouchedAzureSeedStore(bs, name, localBasePath) {
			used, usageErr := refs.inUse(ctx, bs)
			if usageErr != nil {
				return usageErr
			}
			if used || groupMembers[bs.ID] {
				rejected = append(rejected, fmt.Sprintf("%q is already used or referenced", name))
				continue
			}
			candidate := cloneBlobStore(bs)
			candidate.Type = "azure"
			candidate.Config = cloneConfig(desired)
			updates = append(updates, pendingChange{store: candidate})
			continue
		}
		rejected = append(rejected, fmt.Sprintf("%q has an individual configuration or a different storage target", name))
	}
	if len(rejected) > 0 {
		return fmt.Errorf("refusing automatic Azure blob-store target change for %s; create a new store and use the blob-store migration endpoint for repositories that need to move", strings.Join(rejected, ", "))
	}

	for _, bs := range creates {
		if createErr := blobRepo.Create(ctx, bs); createErr != nil {
			return createErr
		}
		log.Info("azure blob store created", "name", bs.Name, "container", az.Container, "account", az.AccountName)
	}
	for _, change := range updates {
		if updateErr := blobRepo.Update(ctx, change.store); updateErr != nil {
			return updateErr
		}
		if reg != nil && change.store.ID != "" {
			reg.Invalidate(change.store.ID)
		}
		if change.credentialRotation {
			log.Info("azure blob store credentials rotated", "name", change.store.Name, "container", az.Container, "account", az.AccountName)
		} else {
			log.Info("azure blob store reconciled", "name", change.store.Name, "container", az.Container, "account", az.AccountName)
		}
	}
	return nil
}

func cloneConfig(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneBlobStore(src *domain.BlobStore) *domain.BlobStore {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Config = cloneConfig(src.Config)
	return &dst
}

func isUntouchedAzureSeedStore(bs *domain.BlobStore, name, localBasePath string) bool {
	if bs == nil || bs.Name != name || bs.Type != "local" || bs.QuotaBytes != nil {
		return false
	}
	if localBasePath == "" {
		localBasePath = "./data/blobs"
	}
	if len(bs.Config) != 1 {
		return false
	}
	path, ok := bs.Config["path"].(string)
	// Fresh databases still carry migration 001's relative paths: the local
	// path sync is intentionally skipped when the selected backend is Azure.
	return ok && (filepath.Clean(path) == filepath.Clean(filepath.Join(localBasePath, name)) ||
		filepath.Clean(path) == filepath.Clean(filepath.Join("./data/blobs", name)))
}

func referencedBlobStoreIDs(stores []domain.BlobStore) map[string]bool {
	refs := make(map[string]bool)
	for _, store := range stores {
		if store.Type != "group" || store.Config == nil {
			continue
		}
		switch members := store.Config["member_ids"].(type) {
		case []string:
			for _, id := range members {
				refs[id] = true
			}
		case []any:
			for _, member := range members {
				if id, ok := member.(string); ok {
					refs[id] = true
				}
			}
		}
	}
	return refs
}

var azureCredentialConfigKeys = map[string]struct{}{
	"account_key":       {},
	"connection_string": {},
	"sas_token":         {},
}

func azureConfigOnlyCredentialsDiffer(cur, desired map[string]any) bool {
	if !azureStorageTargetEqual(cur, desired) {
		return false
	}
	return reflect.DeepEqual(azureNonCredentialConfig(cur), azureNonCredentialConfig(desired))
}

func azureStorageTargetEqual(cur, desired map[string]any) bool {
	return azureStorageTarget(cur)["identity"] != "" && reflect.DeepEqual(azureStorageTarget(cur), azureStorageTarget(desired))
}

func azureNonCredentialConfig(cfg map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		if _, credential := azureCredentialConfigKeys[k]; credential {
			continue
		}
		switch k {
		case "container", "account_name", "endpoint", "skip_tls_verify":
			continue
		default:
			out[k] = v
		}
	}
	target := azureStorageTarget(cfg)
	for k, v := range target {
		out[k] = v
	}
	return out
}

func azureStorageTarget(cfg map[string]any) map[string]any {
	return map[string]any{
		"identity":        storage.PhysicalStoreIdentity(storage.BlobStoreDescriptor{Type: "azure", Config: cfg}),
		"skip_tls_verify": configBool(cfg, "skip_tls_verify"),
	}
}

func configBool(cfg map[string]any, key string) bool {
	value, _ := cfg[key].(bool)
	return value
}

// s3ConfigEqual reports whether cur already holds exactly the desired s3 config.
func s3ConfigEqual(cur, desired map[string]any) bool {
	if len(cur) != len(desired) {
		return false
	}
	for k, dv := range desired {
		if cur[k] != dv {
			return false
		}
	}
	return true
}

// seedPlaceholderAdminHash is the bcrypt hash the initial migration (001) seeds
// for the admin user. Despite the migration comment it is NOT a hash of the
// documented admin123 password — it is a well-known placeholder. While it is
// still in place the operator's configured bootstrap password has never taken
// effect, so bootstrap treats it as "not yet set" and applies the configured
// password. Once the admin password has been changed (API rotation or a prior
// bootstrap correction) this no longer matches and the password is left alone.
const seedPlaceholderAdminHash = "$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VcSAg/ROS"

// seedAdminUsername is the account migration 001 pre-creates.
const seedAdminUsername = "admin"

// warnIfAdminUnusable says so when bootstrap is off but the seeded admin still
// carries the placeholder hash: no password matches it, so on a fresh database
// that combination means nobody can log in. Turning bootstrap off is only safe
// once a real account exists, and an operator who got the order wrong needs to
// hear about it at startup rather than at the login form.
func warnIfAdminUnusable(ctx context.Context, userRepo repository.UserRepo, b config.BootstrapConfig, log logger.Logger) {
	name := b.AdminUsername
	if name == "" {
		name = seedAdminUsername
	}
	admin, err := userRepo.Get(ctx, name)
	if err != nil || admin == nil || admin.PasswordHash != seedPlaceholderAdminHash {
		return
	}
	log.Warn("bootstrap is disabled but the admin account still has the unusable seed password — no password will log in; enable bootstrap once to set one, or create an account another way",
		"username", name)
}

// bootstrapAdmin ensures the admin user exists with the configured password.
func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	authSvc := auth.NewService(cfg.Auth.JWTSecret, cfg.Auth.JWTExpiryHours, cfg.Auth.BcryptCost).
		WithMinPasswordLength(cfg.Auth.PasswordMinLength)
	userRepo := postgres.NewUserRepo(pool)
	roleRepo := postgres.NewRoleRepo(pool)
	return ensureBootstrapAdmin(ctx, userRepo, roleRepo, authSvc, cfg.Bootstrap, log)
}

// ensureBootstrapAdmin creates the admin user on first boot (with the configured
// password and the nx-admin role) and, on subsequent boots, applies the
// configured password only if the stored hash is still the seed placeholder.
// An admin whose password has genuinely been changed is never touched.
//
// bootstrap.enabled=false skips the whole thing, which is how an operator stops
// keeping admin credentials in the config file (#243).
func ensureBootstrapAdmin(
	ctx context.Context,
	userRepo repository.UserRepo,
	roleRepo repository.RoleRepo,
	authSvc *auth.Service,
	b config.BootstrapConfig,
	log logger.Logger,
) error {
	if !b.Enabled || b.AdminUsername == "" || b.AdminPassword == "" {
		warnIfAdminUnusable(ctx, userRepo, b, log)
		log.Info("bootstrap: disabled — the admin account is left as it is")
		return nil
	}

	hash, err := authSvc.HashPassword(b.AdminPassword)
	if err != nil {
		return err
	}

	existing, err := userRepo.Get(ctx, b.AdminUsername)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	if existing == nil {
		// Create fresh admin user
		u := &domain.User{
			Username:     b.AdminUsername,
			Email:        b.AdminEmail,
			FirstName:    b.AdminFirstName,
			PasswordHash: hash,
			Status:       domain.UserStatusActive,
			Source:       domain.UserSourceLocal,
		}
		if err := userRepo.Create(ctx, u); err != nil {
			return err
		}
		// Find nx-admin role and assign it
		adminRole, err := findRoleByName(ctx, roleRepo, "nx-admin")
		if err != nil || adminRole == nil {
			log.Warn("nx-admin role not found — skip role assignment")
		} else {
			_ = roleRepo.SetUserRoles(ctx, u.ID, []string{adminRole.ID})
		}
		log.Info("bootstrap: admin user created", "username", b.AdminUsername)
		return nil
	}

	// Admin already exists. The seed migration pre-creates it with a placeholder
	// hash; while that placeholder is in place the configured password has never
	// applied, so set it now (first-boot correction). Otherwise leave it alone —
	// operators rotate the password via the API, not config + restart.
	if existing.PasswordHash == seedPlaceholderAdminHash {
		if err := userRepo.UpdatePassword(ctx, b.AdminUsername, hash); err != nil {
			return err
		}
		log.Info("bootstrap: admin had the seed placeholder password — applied configured admin_password", "username", b.AdminUsername)
		return nil
	}
	log.Info("bootstrap: admin user already exists — password not modified", "username", b.AdminUsername)
	return nil
}

func findRoleByName(ctx context.Context, repo interface {
	List(context.Context) ([]domain.Role, error)
}, name string) (*domain.Role, error) {
	roles, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // bootstrap-only lookup; nil result signals "no such role" to the caller (no error condition)
}

// Version is injected at build time via -ldflags
var Version = "dev"

// dbHost extracts the host from a postgres DSN URL for safe log display.
func dbHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return dsn
	}
	return u.Host
}
