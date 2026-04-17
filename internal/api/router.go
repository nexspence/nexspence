package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

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

	authSvc := auth.NewService(
		cfg.Auth.JWTSecret,
		cfg.Auth.JWTExpiryHours,
		cfg.Auth.BcryptCost,
	)

	localBlob, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	if err != nil {
		panic("failed to init blob store: " + err.Error())
	}

	repoSvc    := service.NewRepositoryService(repoRepo, blobRepo, localBlob)
	userSvc    := service.NewUserService(userRepo, roleRepo, authSvc)
	cleanupSvc := service.NewCleanupService(cleanupRepo, assetRepo, localBlob, log)

	// Start cleanup scheduler in background (every 6 hours).
	go cleanupSvc.StartScheduler(context.Background(), 6*time.Hour)

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
	authH      := handlers.NewAuthHandler(userSvc)
	repoH      := handlers.NewRepositoryHandler(repoSvc)
	userH      := handlers.NewUserHandler(userSvc)
	blobH      := handlers.NewBlobStoreHandler(blobRepo)
	componentH := handlers.NewComponentHandler(componentRepo, assetRepo, cfg.HTTP.BaseURL)
	cleanupH   := handlers.NewCleanupHandler(cleanupRepo, cleanupSvc)
	auditH     := handlers.NewAuditHandler(auditRepo)

	// ── Gin engine ────────────────────────────────────────────
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger(log))
	r.Use(corsMiddleware())
	r.Use(handlers.MetricsMiddleware())
	r.Use(AuditMiddleware(auditRepo))

	authMW := handlers.AuthMiddleware(userSvc)

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

	// ── Authenticated endpoints ───────────────────────────────
	auth := r.Group("", authMW)
	{
		auth.GET("/service/rest/v1/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "edition": "OSS"})
		})
		auth.GET("/service/rest/v1/status/writable", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		// ── My profile ────────────────────────────────────────
		auth.GET("/api/v1/me", authH.Me)

		// ── Repositories ──────────────────────────────────────
		auth.GET("/service/rest/v1/repositories", repoH.List)
		auth.GET("/service/rest/v1/repositories/:name", repoH.Get)
		auth.POST("/service/rest/v1/repositories/:format/:type", repoH.Create)
		auth.PUT("/service/rest/v1/repositories/:format/:type/:name", repoH.Update)
		auth.DELETE("/service/rest/v1/repositories/:name", repoH.Delete)

		// Nexspence native repos API
		auth.GET("/api/v1/repositories", repoH.List)
		auth.POST("/api/v1/repositories", func(c *gin.Context) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "use /service/rest/v1/repositories/:format/:type"})
		})

		// ── Blob stores ───────────────────────────────────────
		auth.GET("/service/rest/v1/blobstores", blobH.List)
		auth.GET("/service/rest/v1/blobstores/:name", blobH.Get)
		auth.POST("/service/rest/v1/blobstores/:type", blobH.Create)
		auth.PUT("/service/rest/v1/blobstores/:type/:name", blobH.Update)
		auth.DELETE("/service/rest/v1/blobstores/:name", blobH.Delete)

		// ── Users ─────────────────────────────────────────────
		auth.GET("/service/rest/v1/security/users", userH.List)
		auth.GET("/service/rest/v1/security/users/:userId", userH.Get)
		auth.POST("/service/rest/v1/security/users", userH.Create)
		auth.PUT("/service/rest/v1/security/users/:userId", userH.Update)
		auth.DELETE("/service/rest/v1/security/users/:userId", userH.Delete)
		auth.PUT("/service/rest/v1/security/users/:userId/change-password", userH.ChangePassword)

		// ── Roles ─────────────────────────────────────────────
		auth.GET("/service/rest/v1/security/roles", func(c *gin.Context) {
			roles, err := roleRepo.List(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			if roles == nil {
				roles = []domain.Role{}
			}
			c.JSON(http.StatusOK, roles)
		})

		// ── Components & Assets ───────────────────────────────
		auth.GET("/service/rest/v1/components", componentH.List)
		auth.GET("/service/rest/v1/components/:id", componentH.Get)
		auth.DELETE("/service/rest/v1/components/:id", componentH.Delete)
		auth.GET("/service/rest/v1/assets", func(c *gin.Context) {
			componentH.SearchAssets(c)
		})
		auth.GET("/service/rest/v1/assets/:id", func(c *gin.Context) {
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
		auth.DELETE("/service/rest/v1/assets/:id", func(c *gin.Context) {
			if err := assetRepo.Delete(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.Status(http.StatusNoContent)
		})

		// ── Search ────────────────────────────────────────────
		auth.GET("/service/rest/v1/search", componentH.Search)
		auth.GET("/service/rest/v1/search/assets", componentH.SearchAssets)
		auth.GET("/service/rest/v1/search/assets/download", stubHandler("search-download"))

		// ── Cleanup policies ──────────────────────────────────
		auth.GET("/service/rest/v1/cleanup-policies", cleanupH.List)
		auth.GET("/service/rest/v1/cleanup-policies/:id", cleanupH.Get)
		auth.POST("/service/rest/v1/cleanup-policies", cleanupH.Create)
		auth.PUT("/service/rest/v1/cleanup-policies/:id", cleanupH.Update)
		auth.DELETE("/service/rest/v1/cleanup-policies/:id", cleanupH.Delete)
		auth.POST("/service/rest/v1/cleanup-policies/:id/run", cleanupH.Run)

		// ── Audit log ─────────────────────────────────────────
		auth.GET("/service/rest/v1/audit", auditH.List)

		auth.GET("/service/rest/v1/tasks", stubHandler("tasks"))
		auth.POST("/service/rest/v1/tasks/:id/run", stubHandler("tasks"))
		auth.GET("/service/rest/v1/security/ldap", stubHandler("ldap"))
		auth.GET("/service/rest/v1/routing-rules", stubHandler("routing"))

		// Migration
		auth.GET("/api/v1/migration/jobs", stubHandler("migration"))
		auth.POST("/api/v1/migration/jobs", stubHandler("migration"))
		auth.POST("/api/v1/migration/jobs/:id/pause", stubHandler("migration"))
		auth.POST("/api/v1/migration/jobs/:id/resume", stubHandler("migration"))
		auth.DELETE("/api/v1/migration/jobs/:id", stubHandler("migration"))

		// System info
		auth.GET("/api/v1/system/info", func(c *gin.Context) {
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
	repo := r.Group("/repository/:repoName", handlers.OptionalAuth(userSvc))
	{
		repo.Any("/*path", func(c *gin.Context) {
			repoName := c.Param("repoName")
			ctx := c.Request.Context()

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

	v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc))
	v2docker.Any("/:repoName/*dockerpath", func(c *gin.Context) {
		repoName := c.Param("repoName")
		dockerPath := c.Param("dockerpath") // e.g. /da/devops/alpine/manifests/3.22.1
		ctx := c.Request.Context()

		repoDef, err := repoRepo.Get(ctx, repoName)
		if err != nil || repoDef == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
			return
		}
		if !repoDef.Online {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
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
	r.NoRoute(serveUI(cfg))

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
