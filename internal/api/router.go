package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/formats/docker"
	"github.com/nexspence-oss/nexspence/internal/formats/gomod"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// NewRouter wires all routes and returns a ready http.Handler.
func NewRouter(cfg *config.Config, pool *pgxpool.Pool, log logger.Logger) http.Handler {
	if cfg.Log.Level != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	// ── Repositories / services ───────────────────────────────
	repoRepo      := postgres.NewRepositoryRepo(pool)
	blobRepo      := postgres.NewBlobStoreRepo(pool)
	userRepo      := postgres.NewUserRepo(pool)
	roleRepo      := postgres.NewRoleRepo(pool)
	componentRepo := postgres.NewComponentRepo(pool)
	assetRepo     := postgres.NewAssetRepo(pool)
	cleanupRepo   := postgres.NewCleanupPolicyRepo(pool)
	auditRepo     := postgres.NewAuditRepo(pool)
	userTokenRepo := postgres.NewUserTokenRepo(pool)
	webhookRepo    := postgres.NewWebhookRepo(pool)
	migrationRepo  := postgres.NewMigrationRepo(pool)
	privilegeRepo  := postgres.NewPrivilegeRepo(pool)
	csRepo        := postgres.NewContentSelectorRepo(pool)
	rbacRepo      := postgres.NewRBACRepo(pool)
	selectorSvc, svcErr := service.NewContentSelectorService(csRepo)
	if svcErr != nil {
		panic("content selector service init: " + svcErr.Error())
	}

	authSvc := auth.NewService(
		cfg.Auth.JWTSecret,
		cfg.Auth.JWTExpiryHours,
		cfg.Auth.BcryptCost,
	)

	localBlob, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	if err != nil {
		panic("failed to init blob store: " + err.Error())
	}

	repoSvc    := service.NewRepositoryService(repoRepo, blobRepo, localBlob, cleanupRepo)
	userSvc    := service.NewUserService(userRepo, roleRepo, authSvc, log)
	var ldapSvc auth.LDAPAuthenticator
	if svc := auth.NewLDAPService(cfg.LDAP); svc != nil {
		ldapSvc = svc
		userSvc.WithLDAP(svc, cfg.LDAP.AdminGroup)
	}

	// OIDC is optional; NewOIDCService performs discovery and will fail
	// startup if the IdP is unreachable or misconfigured (loud > lazy).
	var oidcSvc auth.OIDCAuthenticator
	var oidcSealer *auth.CookieSealer
	if cfg.OIDC.Enabled {
		svc, err := auth.NewOIDCService(context.Background(), cfg.OIDC)
		if err != nil {
			panic("oidc init: " + err.Error())
		}
		oidcSvc = svc
		keyBytes, decErr := base64.StdEncoding.DecodeString(cfg.OIDC.CookieKey)
		if decErr != nil {
			panic("oidc cookie_key base64: " + decErr.Error())
		}
		sealer, sErr := auth.NewCookieSealer(keyBytes)
		if sErr != nil {
			panic("oidc cookie sealer: " + sErr.Error())
		}
		oidcSealer = sealer
		userSvc.WithOIDC(oidcSvc, cfg.OIDC)
	}
	tokenSvc   := service.NewTokenService(userTokenRepo, userRepo)
	webhookSvc := service.NewWebhookService(webhookRepo)
	repoSvc.WithWebhooks(webhookSvc)
	cleanupSvc := service.NewCleanupService(cleanupRepo, repoRepo, assetRepo, blobRepo, localBlob, log)

	// Start per-policy cron scheduler in background (default: cfg.Cleanup.DefaultSchedule).
	go cleanupSvc.StartCronScheduler(context.Background(), cfg.Cleanup.DefaultSchedule)

	// ── Format handlers ───────────────────────────────────────
	formatDeps := formats.Deps{
		Repos:      repoRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		Blobs:      blobRepo,
		BlobStore:  localBlob,
		BaseURL:    cfg.HTTP.BaseURL,
		Webhooks:   webhookSvc,
	}
	formatRegistry := map[string]formats.FormatHandler{
		"raw":    raw.New(formatDeps),
		"maven2": maven.New(formatDeps),
		"npm":    npm.New(formatDeps),
		"pypi":   pypi.New(formatDeps),
		"go":     gomod.New(formatDeps),
		"helm":   helm.New(formatDeps),
		"nuget":  nuget.New(formatDeps),
		"cargo":  cargo.New(formatDeps),
		"conan":  conan.New(formatDeps),
		"apt":    apt.New(formatDeps),
		"yum":    yum.New(formatDeps),
		"docker": docker.New(formatDeps),
	}
	// Group handler needs a reference to the registry to fan-out to members.
	groupHandler := group.New(formatDeps, formatRegistry)

	// ── Handlers ──────────────────────────────────────────────
	authH      := handlers.NewAuthHandler(userSvc, log).WithConfig(*cfg)
	rbacSvc    := service.NewRBACService(rbacRepo, repoRepo, log)
	repoH      := handlers.NewRepositoryHandler(repoSvc, rbacSvc)
	userH      := handlers.NewUserHandler(userSvc)
	blobH      := handlers.NewBlobStoreHandler(blobRepo).WithUsageDeps(repoRepo, assetRepo)
	componentH := handlers.NewComponentHandler(componentRepo, assetRepo, repoRepo, cfg.HTTP.BaseURL).WithRBAC(rbacSvc)
	browseH    := handlers.NewBrowseHandler(repoRepo, componentRepo, assetRepo, blobRepo, localBlob, rbacSvc)
	cleanupH   := handlers.NewCleanupHandler(cleanupRepo, repoRepo, cleanupSvc)
	auditH     := handlers.NewAuditHandler(auditRepo)
	scanSvc    := service.NewScanService(componentRepo, cfg.HTTP.BaseURL)
	scanH      := handlers.NewScanHandler(scanSvc)
	tokenH     := handlers.NewTokenHandler(tokenSvc, userSvc, cfg.Auth.TokenMaxDays)
	webhookH   := handlers.NewWebhookHandler(webhookSvc)
	roleH      := handlers.NewRoleHandler(roleRepo, userRepo)
	privH      := handlers.NewPrivilegeHandler(privilegeRepo, roleRepo)
	csH        := handlers.NewContentSelectorHandler(selectorSvc)
	systemH    := handlers.NewSystemHandler(cfg, pool, ldapSvc, oidcSvc)
	migrationH := handlers.NewMigrationHandler(migrationRepo)
	rbacMW     := handlers.RBACMiddleware(rbacSvc, repoRepo)

	// ── Gin engine ────────────────────────────────────────────
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger(log))
	r.Use(corsMiddleware())
	r.Use(handlers.MetricsMiddleware())
	r.Use(AuditMiddleware(auditRepo))

	authMW  := handlers.AuthMiddleware(userSvc, tokenSvc)
	adminMW := handlers.AdminRequired()

	// ── Public endpoints ──────────────────────────────────────
	r.GET("/service/rest/v1/status/check", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Auth
	r.POST("/api/v1/login", authH.Login)
	// Nexus-compat login (used by some clients)
	r.POST("/service/rest/v1/security/users/login", authH.Login)

	// Public: feature-detection for LoginPage (whether SSO button should render).
	r.GET("/api/v1/auth/config", authH.Config)

	// Public: token creation policy (max expiry days).
	r.GET("/api/v1/auth/token-policy", tokenH.TokenPolicy)

	// OIDC redirect flow — only registered when oidc.enabled=true.
	// Audit events fire via the global AuditMiddleware above (callback GET
	// whitelisted). Routes are public so the pre-auth redirect flow works.
	var oidcH *handlers.OIDCHandler
	if oidcSvc != nil && oidcSealer != nil {
		oidcH = handlers.NewOIDCHandler(oidcSvc, userSvc, userRepo, oidcSealer, cfg.OIDC, log)
		r.GET("/api/v1/auth/oidc/login", oidcH.Login)
		r.GET("/api/v1/auth/oidc/callback", oidcH.Callback)
	}

	// Metrics (public — useful for monitoring without auth)
	r.GET("/api/v1/metrics", handlers.MetricsHandler)

	// ── Authenticated endpoints (all valid users) ────────────
	authed := r.Group("", authMW)
	{
		authed.GET("/service/rest/v1/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "edition": "OSS"})
		})
		authed.GET("/service/rest/v1/status/writable", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		// ── My profile ────────────────────────────────────────
		authed.GET("/api/v1/me", authH.Me)
		authed.GET("/api/v1/me/privileges", privH.MyPrivileges)

		// ── Repositories (read) ───────────────────────────────
		authed.GET("/service/rest/v1/repositories", repoH.List)
		authed.GET("/service/rest/v1/repositories/:name", repoH.Get)
		authed.GET("/api/v1/repositories", repoH.List)
		authed.GET("/api/v1/repositories/:name/quota", componentH.GetQuota)

		// ── Browse ────────────────────────────────────────────
		authed.GET("/api/v1/browse/repositories/:name/docker-tree", browseH.DockerTree)
		authed.GET("/api/v1/browse/repositories/:name/raw-tree", browseH.RawTree)
		authed.GET("/api/v1/browse/repositories/:name/path-tree", browseH.PathTree)
		authed.DELETE("/api/v1/browse/repositories/:name/path", browseH.DeleteByPath)
		authed.DELETE("/api/v1/browse/repositories/:name/docker-tag", browseH.DeleteDockerTag)
		authed.DELETE("/api/v1/browse/repositories/:name/docker-image", browseH.DeleteDockerImage)

		// ── Components & Assets (read + search) ───────────────
		authed.GET("/service/rest/v1/components", componentH.List)
		authed.GET("/service/rest/v1/components/:id", componentH.Get)
		authed.GET("/service/rest/v1/assets", func(c *gin.Context) {
			componentH.SearchAssets(c)
		})
		authed.GET("/service/rest/v1/assets/:id", func(c *gin.Context) {
			id := c.Param("id")
			a, err := assetRepo.Get(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			if a == nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "asset not found"})
				return
			}
			a.DownloadURL = cfg.HTTP.BaseURL + "/repository/" + a.Repository + a.Path
			c.JSON(http.StatusOK, a)
		})

		// ── Search ────────────────────────────────────────────
		authed.GET("/service/rest/v1/search", componentH.Search)
		authed.GET("/service/rest/v1/search/assets", componentH.SearchAssets)
		authed.GET("/service/rest/v1/search/assets/download", stubHandler("search-download"))

		// ── API tokens (current user) ─────────────────────────
		authed.GET("/api/v1/tokens", tokenH.List)
		authed.POST("/api/v1/tokens", tokenH.Create)
		authed.DELETE("/api/v1/tokens/:id", tokenH.Delete)

		// ── OIDC logout (authenticated users only) ────────────
		if oidcH != nil {
			authed.GET("/api/v1/auth/oidc/logout", oidcH.Logout)
		}

		// ── Vulnerability scan (read) ─────────────────────────
		authed.GET("/api/v1/components/:id/scan", scanH.GetScanResult)

		// ── Blob stores (read) ────────────────────────────────
		authed.GET("/service/rest/v1/blobstores", blobH.List)
		authed.GET("/service/rest/v1/blobstores/:name", blobH.Get)
		authed.GET("/api/v1/blob-stores/:name/usage", blobH.Usage)

		// ── Cleanup policies (read) ───────────────────────────
		authed.GET("/service/rest/v1/cleanup-policies", cleanupH.List)
		authed.GET("/service/rest/v1/cleanup-policies/:id", cleanupH.Get)

		// ── Roles (read) ──────────────────────────────────────
		authed.GET("/service/rest/v1/security/roles", roleH.List)

		// ── Privileges (read) ─────────────────────────────────
		authed.GET("/service/rest/v1/security/privileges", privH.List)
		authed.GET("/service/rest/v1/security/privileges/:id", privH.Get)
		authed.GET("/service/rest/v1/security/roles/:id/privileges", privH.ListRolePrivileges)
		authed.GET("/api/v1/security/privilege-role-map", privH.RoleMap)

		// ── Content Selectors (read) ──────────────────────────
		authed.GET("/service/rest/v1/security/content-selectors", csH.List)
		authed.GET("/service/rest/v1/security/content-selectors/:id", csH.Get)
	}

	// ── Admin-only endpoints (nx-admin role required) ─────────
	admin := r.Group("", authMW, adminMW)
	{
		// ── Repositories (write) ──────────────────────────────
		admin.POST("/service/rest/v1/repositories/:format/:type", repoH.Create)
		admin.PUT("/service/rest/v1/repositories/:format/:type/:name", repoH.Update)
		admin.PATCH("/service/rest/v1/repositories/:name", repoH.Patch)
		admin.DELETE("/service/rest/v1/repositories/:name", repoH.Delete)
		admin.POST("/api/v1/repositories", func(c *gin.Context) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "use /service/rest/v1/repositories/:format/:type"})
		})

		// ── Blob stores (write) ───────────────────────────────
		admin.POST("/service/rest/v1/blobstores/:type", blobH.Create)
		admin.PUT("/service/rest/v1/blobstores/:type/:name", blobH.Update)
		admin.DELETE("/service/rest/v1/blobstores/:name", blobH.Delete)

		// ── Users ─────────────────────────────────────────────
		admin.GET("/service/rest/v1/security/users", userH.List)
		admin.GET("/service/rest/v1/security/users/:userId", userH.Get)
		admin.POST("/service/rest/v1/security/users", userH.Create)
		admin.PUT("/service/rest/v1/security/users/:userId", userH.Update)
		admin.DELETE("/service/rest/v1/security/users/:userId", userH.Delete)
		admin.PUT("/service/rest/v1/security/users/:userId/change-password", userH.ChangePassword)

		// ── Components & Assets (delete) ──────────────────────
		admin.DELETE("/service/rest/v1/components/:id", componentH.Delete)
		admin.PUT("/service/rest/v1/components/:id/tags", componentH.SetTags)
		admin.DELETE("/service/rest/v1/assets/:id", func(c *gin.Context) {
			if err := assetRepo.Delete(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.Status(http.StatusNoContent)
		})

		// ── Cleanup policies (write + run) ────────────────────
		admin.POST("/service/rest/v1/cleanup-policies", cleanupH.Create)
		admin.PUT("/service/rest/v1/cleanup-policies/:id", cleanupH.Update)
		admin.DELETE("/service/rest/v1/cleanup-policies/:id", cleanupH.Delete)
		admin.POST("/service/rest/v1/cleanup-policies/:id/run", cleanupH.Run)

		// ── Roles (write) ─────────────────────────────────────
		admin.POST("/service/rest/v1/security/roles", roleH.Create)
		admin.PUT("/service/rest/v1/security/roles/:id", roleH.Update)
		admin.DELETE("/service/rest/v1/security/roles/:id", roleH.Delete)
		admin.PUT("/service/rest/v1/security/users/:userId/roles", roleH.SetUserRoles)

		// ── Privileges (write) ────────────────────────────────
		admin.POST("/service/rest/v1/security/privileges", privH.Create)
		admin.PUT("/service/rest/v1/security/privileges/:id", privH.Update)
		admin.DELETE("/service/rest/v1/security/privileges/:id", privH.Delete)
		admin.PUT("/service/rest/v1/security/roles/:id/privileges", privH.SetRolePrivileges)

		// ── Content Selectors (write) ─────────────────────────
		admin.POST("/service/rest/v1/security/content-selectors", csH.Create)
		admin.PUT("/service/rest/v1/security/content-selectors/:id", csH.Update)
		admin.DELETE("/service/rest/v1/security/content-selectors/:id", csH.Delete)
		admin.PUT("/service/rest/v1/security/privileges/:id/content-selector/:selectorId", csH.AttachToPrivilege)
		admin.DELETE("/service/rest/v1/security/privileges/:id/content-selector", csH.DetachFromPrivilege)

		// ── Webhooks (admin) ──────────────────────────────────
		admin.GET("/api/v1/webhooks", webhookH.List)
		admin.GET("/api/v1/webhooks/:id", webhookH.Get)
		admin.POST("/api/v1/webhooks", webhookH.Create)
		admin.PUT("/api/v1/webhooks/:id", webhookH.Update)
		admin.DELETE("/api/v1/webhooks/:id", webhookH.Delete)
		admin.POST("/api/v1/webhooks/:id/test", webhookH.Test)

		// ── Audit log ─────────────────────────────────────────
		admin.GET("/service/rest/v1/audit", auditH.List)

		// ── Vulnerability scan (trigger) ──────────────────────
		admin.POST("/api/v1/components/:id/scan", scanH.Scan)

		// ── System ────────────────────────────────────────────
		admin.GET("/service/rest/v1/tasks", stubHandler("tasks"))
		admin.POST("/service/rest/v1/tasks/:id/run", stubHandler("tasks"))
		admin.GET("/service/rest/v1/security/ldap", stubHandler("ldap"))
		admin.GET("/service/rest/v1/routing-rules", stubHandler("routing"))

		// Migration
		admin.GET("/api/v1/migration/jobs", migrationH.ListJobs)
		admin.POST("/api/v1/migration/jobs", migrationH.CreateJob)
		admin.GET("/api/v1/migration/jobs/:id", migrationH.GetJob)
		admin.POST("/api/v1/migration/jobs/:id/pause", migrationH.PauseJob)
		admin.POST("/api/v1/migration/jobs/:id/resume", migrationH.ResumeJob)
		admin.DELETE("/api/v1/migration/jobs/:id", migrationH.DeleteJob)

		// System info + service health
		admin.GET("/api/v1/system/info", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"version": "1.0.0",
				"edition": "OSS",
				"product": "Nexspence",
			})
		})
		admin.GET("/api/v1/system/services", systemH.Services)
	}

	// ── Artifact protocol endpoints ───────────────────────────
	// Route /repository/:repoName/* to the appropriate format handler.
	// The format is looked up from the repository record in the DB.
	repo := r.Group("/repository/:repoName", handlers.OptionalAuth(userSvc, tokenSvc), rbacMW)
	{
		repo.Any("/*path", func(c *gin.Context) {
			repoName := c.Param("repoName")
			ctx := c.Request.Context()
			if uid, ok := c.Get("userID"); ok {
				if id, ok2 := uid.(string); ok2 && id != "" {
					uname, _ := c.Get("username")
					uStr, _ := uname.(string)
					ctx = requestctx.WithUser(ctx, id, uStr)
				}
			}
			c.Request = c.Request.WithContext(ctx)

			repoDef, err := repoRepo.Get(ctx, repoName)
			if err != nil || repoDef == nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
				return
			}
			if !repoDef.Online {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
				return
			}

			// Group repositories fan-out to member repos.
			if repoDef.Type == domain.TypeGroup {
				groupHandler.ServeHTTP(c)
				return
			}

			handler, ok := formatRegistry[string(repoDef.Format)]
			if !ok {
				c.JSON(http.StatusNotImplemented, gin.H{
					"error": "format not yet implemented: " + string(repoDef.Format),
				})
				return
			}

			handler.ServeHTTP(c)
		})
	}

	// ── Docker registry v2 API ────────────────────────────────
	// Two URL styles are supported (both fully functional):
	//   Long:  docker tag <image> localhost:8081/repository/<repoName>/<image>:<tag>
	//          API at /v2/repository/:repoName/*
	//   Short: docker tag <image> localhost:8081/<repoName>/<image>:<tag>
	//          API at /v2/:repoName/*
	// Gin static-segment priority ensures /v2/repository/... always matches the long-path
	// group first; the short-path group catches everything else under /v2/.
	// GET/HEAD /v2/ — OCI version check + Basic auth challenge.
	// Returning 200 unconditionally makes Docker treat the registry as public and
	// silently drop stored credentials on subsequent requests — which then fail RBAC
	// as "anonymous" and surface to users as "pull access denied" even though
	// `docker login` reported success. DockerV2Auth validates credentials when
	// present and issues a 401 + `WWW-Authenticate: Basic` challenge otherwise,
	// so `docker login` actually verifies the password and the CLI sends it on
	// /v2/:repoName/* requests.
	// `repoRepo` lets DockerV2Auth fall through to 200 when at least one
	// Docker repository has allow_anonymous=true — restoring anonymous
	// `docker pull` against public proxies (see Phase 26).
	dockerV2Root := handlers.DockerV2Auth(userSvc, tokenSvc, repoRepo)
	r.GET("/v2/", dockerV2Root)
	r.HEAD("/v2/", dockerV2Root)

	dockerV2H := serveDockerV2(repoRepo, groupHandler, formatRegistry)
	v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2docker.Any("/:repoName/*dockerpath", dockerV2H)

	// Short-path Docker: /v2/:repoName/* — no "repository/" segment required.
	// Gin static segments take priority, so /v2/repository/:repoName/... still
	// matches v2docker above; this group catches everything else under /v2/.
	v2short := r.Group("/v2", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2short.Any("/:repoName/*dockerpath", dockerV2H)

	// ── Frontend static (production build) ────────────────────
	ui := serveUI(cfg)
	r.NoRoute(func(c *gin.Context) {
		ui(c)
	})

	return r
}

// serveDockerV2 returns a gin.HandlerFunc that dispatches Docker OCI v2 API
// requests for a named repository. Registered on both the long-path route
// (/v2/repository/:repoName/*dockerpath) and the short-path route
// (/v2/:repoName/*dockerpath) so they share identical dispatch logic.
func serveDockerV2(
	repoRepo repository.RepositoryRepo,
	groupH formats.FormatHandler,
	fmtRegistry map[string]formats.FormatHandler,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		repoName := c.Param("repoName")
		dockerPath := c.Param("dockerpath") // e.g. /da/devops/alpine/manifests/3.22.1
		ctx := c.Request.Context()
		if uid, ok := c.Get("userID"); ok {
			if id, ok2 := uid.(string); ok2 && id != "" {
				uname, _ := c.Get("username")
				uStr, _ := uname.(string)
				ctx = requestctx.WithUser(ctx, id, uStr)
			}
		}
		c.Request = c.Request.WithContext(ctx)

		repoDef, err := repoRepo.Get(ctx, repoName)
		if err != nil || repoDef == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
			return
		}
		if !repoDef.Online {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
			return
		}

		if repoDef.Type == domain.TypeGroup {
			if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
				c.JSON(http.StatusMethodNotAllowed, gin.H{
					"error": "group repository is read-only — publish to a member hosted repository",
				})
				return
			}
			if string(repoDef.Format) != "docker" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a docker registry"})
				return
			}
			c.Params = gin.Params{
				{Key: "repoName", Value: repoName},
				{Key: "path", Value: "/v2" + dockerPath},
			}
			groupH.ServeHTTP(c)
			return
		}

		if string(repoDef.Format) != "docker" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a docker registry"})
			return
		}

		// Rewrite params so the docker handler sees what it expects:
		//   c.Param("repoName") = <repoName>
		//   c.Param("path")     = /v2/<imagePath>/<endpoint>
		c.Params = gin.Params{
			{Key: "repoName", Value: repoName},
			{Key: "path", Value: "/v2" + dockerPath},
		}
		fmtRegistry["docker"].ServeHTTP(c)
	}
}

func stubHandler(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"error": name + " not yet implemented"})
	}
}

func serveUI(cfg *config.Config) gin.HandlerFunc {
	candidates := []string{
		"./frontend/dist",
		"/app/frontend/dist",
	}

	var distDir string
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			distDir = dir
			break
		}
	}

	if distDir == "" {
		return func(c *gin.Context) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!DOCTYPE html>
<html style="background:#070b14;color:#e5e7eb;font-family:system-ui;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
<body style="text-align:center">
  <div>
    <div style="font-size:48px;margin-bottom:16px">📦</div>
    <h1 style="color:#dbeafe;margin-bottom:8px">Nexspence</h1>
    <p style="color:rgba(229,231,235,0.6);margin-bottom:24px">Frontend not built yet</p>
    <code style="background:rgba(255,255,255,0.08);padding:10px 16px;border-radius:8px;color:#93c5fd">
      cd frontend &amp;&amp; npm run build
    </code>
    <p style="margin-top:24px;color:rgba(229,231,235,0.4);font-size:13px">
      API: <a href="/service/rest/v1/status/check" style="color:#3b82f6">/service/rest/v1/status/check</a>
    </p>
  </div>
</body></html>`))
		}
	}

	fs := http.Dir(distDir)
	fileServer := http.FileServer(fs)

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if _, err := os.Stat(filepath.Join(distDir, path)); err != nil {
			c.File(filepath.Join(distDir, "index.html"))
			return
		}
		c.Request.URL.Path = path
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
