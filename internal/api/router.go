package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	webhookRepo   := postgres.NewWebhookRepo(pool)
	privilegeRepo := postgres.NewPrivilegeRepo(pool)
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
	if ldapSvc := auth.NewLDAPService(cfg.LDAP); ldapSvc != nil {
		userSvc.WithLDAP(ldapSvc, cfg.LDAP.AdminGroup)
	}
	tokenSvc   := service.NewTokenService(userTokenRepo, userRepo)
	webhookSvc := service.NewWebhookService(webhookRepo)
	cleanupSvc := service.NewCleanupService(cleanupRepo, repoRepo, assetRepo, localBlob, log)

	// Start cleanup scheduler in background (every 6 hours).
	go cleanupSvc.StartCronScheduler(context.Background(), cfg.Cleanup.DefaultSchedule)

	// ── Format handlers ───────────────────────────────────────
	formatDeps := formats.Deps{
		Repos:      repoRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		Blobs:      blobRepo,
		BlobStore:  localBlob,
		BaseURL:    cfg.HTTP.BaseURL,
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
	authH      := handlers.NewAuthHandler(userSvc, log)
	rbacSvc    := service.NewRBACService(rbacRepo, repoRepo)
	repoH      := handlers.NewRepositoryHandler(repoSvc, rbacSvc)
	userH      := handlers.NewUserHandler(userSvc)
	blobH      := handlers.NewBlobStoreHandler(blobRepo)
	componentH := handlers.NewComponentHandler(componentRepo, assetRepo, repoRepo, cfg.HTTP.BaseURL)
	browseH    := handlers.NewBrowseHandler(repoRepo, componentRepo, assetRepo)
	cleanupH   := handlers.NewCleanupHandler(cleanupRepo, repoRepo, cleanupSvc)
	auditH     := handlers.NewAuditHandler(auditRepo)
	scanSvc    := service.NewScanService(componentRepo, cfg.HTTP.BaseURL)
	scanH      := handlers.NewScanHandler(scanSvc)
	tokenH     := handlers.NewTokenHandler(tokenSvc, userSvc)
	webhookH   := handlers.NewWebhookHandler(webhookSvc)
	roleH      := handlers.NewRoleHandler(roleRepo, userRepo)
	privH      := handlers.NewPrivilegeHandler(privilegeRepo, roleRepo)
	csH        := handlers.NewContentSelectorHandler(selectorSvc)
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

		// ── Repositories (read) ───────────────────────────────
		authed.GET("/service/rest/v1/repositories", repoH.List)
		authed.GET("/service/rest/v1/repositories/:name", repoH.Get)
		authed.GET("/api/v1/repositories", repoH.List)

		// ── Browse ────────────────────────────────────────────
		authed.GET("/api/v1/browse/repositories/:name/docker-tree", browseH.DockerTree)
		authed.GET("/api/v1/browse/repositories/:name/path-tree", browseH.PathTree)

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

		// ── Vulnerability scan (read) ─────────────────────────
		authed.GET("/api/v1/components/:id/scan", scanH.GetScanResult)

		// ── Blob stores (read) ────────────────────────────────
		authed.GET("/service/rest/v1/blobstores", blobH.List)
		authed.GET("/service/rest/v1/blobstores/:name", blobH.Get)

		// ── Cleanup policies (read) ───────────────────────────
		authed.GET("/service/rest/v1/cleanup-policies", cleanupH.List)
		authed.GET("/service/rest/v1/cleanup-policies/:id", cleanupH.Get)

		// ── Roles (read) ──────────────────────────────────────
		authed.GET("/service/rest/v1/security/roles", roleH.List)

		// ── Privileges (read) ─────────────────────────────────
		authed.GET("/service/rest/v1/security/privileges", privH.List)
		authed.GET("/service/rest/v1/security/privileges/:id", privH.Get)
		authed.GET("/service/rest/v1/security/roles/:id/privileges", privH.ListRolePrivileges)

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
		admin.POST("/api/v1/webhooks", webhookH.Create)
		admin.PUT("/api/v1/webhooks/:id", webhookH.Update)
		admin.DELETE("/api/v1/webhooks/:id", webhookH.Delete)

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
		admin.GET("/api/v1/migration/jobs", stubHandler("migration"))
		admin.POST("/api/v1/migration/jobs", stubHandler("migration"))
		admin.POST("/api/v1/migration/jobs/:id/pause", stubHandler("migration"))
		admin.POST("/api/v1/migration/jobs/:id/resume", stubHandler("migration"))
		admin.DELETE("/api/v1/migration/jobs/:id", stubHandler("migration"))

		// System info
		admin.GET("/api/v1/system/info", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"version": "1.0.0",
				"edition": "OSS",
				"product": "Nexspence",
			})
		})
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
	// Docker clients tag images as localhost:8081/repository/<repoName>/<image>:<tag>
	// and send all API requests to /v2/repository/<repoName>/...
	// The version check must be public (Docker does GET /v2/ before sending credentials).
	r.GET("/v2/", func(c *gin.Context) {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")
		c.Status(http.StatusOK)
	})
	r.HEAD("/v2/", func(c *gin.Context) {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")
		c.Status(http.StatusOK)
	})

	v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2docker.Any("/:repoName/*dockerpath", func(c *gin.Context) {
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
			groupHandler.ServeHTTP(c)
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
		formatRegistry["docker"].ServeHTTP(c)
	})

	// ── Frontend static (production build) ────────────────────
	ui := serveUI(cfg)
	r.NoRoute(func(c *gin.Context) {
		// Docker pull localhost:8081/dockerproxy/... → /v2/dockerproxy/... (no "repository/" segment)
		// would otherwise fall through to the SPA (text/html) and break layer unpack.
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/v2/") && p != "/v2/" && !strings.HasPrefix(p, "/v2/repository/") {
			c.Header("Content-Type", "application/json")
			c.JSON(http.StatusNotFound, gin.H{
				"errors": []gin.H{{
					"code": "NAME_UNKNOWN",
					"message": "Nexspence Docker v2 API is only at /v2/repository/<repoName>/... " +
						"— the image reference must contain the literal segment `repository` after the host. " +
						"Example: docker pull " + strings.TrimSuffix(cfg.HTTP.BaseURL, "/") +
						"/repository/dockerproxy/library/alpine:latest",
				}},
			})
			return
		}
		ui(c)
	})

	return r
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
