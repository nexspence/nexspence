package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
)

// ServiceStatus describes the live health of one external dependency.
type ServiceStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"`              // "ok" | "error" | "disabled"
	LatencyMs int    `json:"latency_ms,omitempty"`
	Detail    string `json:"detail"`
	CheckedAt string `json:"checked_at"`
}

// SystemHandler serves system-level diagnostic endpoints.
type SystemHandler struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	ldap auth.LDAPAuthenticator  // nil when LDAP disabled
	oidc auth.OIDCAuthenticator  // nil when OIDC disabled
}

func NewSystemHandler(cfg *config.Config, pool *pgxpool.Pool, ldap auth.LDAPAuthenticator, oidc auth.OIDCAuthenticator) *SystemHandler {
	return &SystemHandler{cfg: cfg, pool: pool, ldap: ldap, oidc: oidc}
}

// Services handles GET /api/v1/system/services.
// Runs each check concurrently with a 5-second timeout.
func (h *SystemHandler) Services(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	type checkFn func(context.Context) ServiceStatus
	checks := []checkFn{
		h.checkPostgres,
		h.checkStorage,
	}
	if h.ldap != nil {
		checks = append(checks, h.checkLDAP)
	} else if h.cfg.LDAP.Enabled {
		checks = append(checks, func(_ context.Context) ServiceStatus {
			return disabled("LDAP")
		})
	}
	if h.oidc != nil {
		checks = append(checks, h.checkOIDC)
	} else if h.cfg.OIDC.Enabled {
		checks = append(checks, func(_ context.Context) ServiceStatus {
			return disabled("OIDC")
		})
	}

	results := make([]ServiceStatus, len(checks))
	var wg sync.WaitGroup
	for i, fn := range checks {
		wg.Add(1)
		go func(idx int, f checkFn) {
			defer wg.Done()
			results[idx] = f(ctx)
		}(i, fn)
	}
	wg.Wait()

	c.JSON(http.StatusOK, results)
}

func (h *SystemHandler) checkPostgres(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.pool.Ping(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	detail := dsnDetail(h.cfg.Database.DSN)
	stat := h.pool.Stat()
	if stat.TotalConns() > 0 {
		detail = fmt.Sprintf("%s · pool %d/%d", detail, stat.AcquiredConns(), stat.TotalConns())
	}

	if err != nil {
		return ServiceStatus{Name: "PostgreSQL", Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: "PostgreSQL", Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkStorage(ctx context.Context) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	if h.cfg.Storage.DefaultType == "s3" {
		s3 := h.cfg.Storage.S3
		detail := fmt.Sprintf("bucket=%s region=%s", s3.Bucket, s3.Region)
		if s3.Endpoint != "" {
			detail = fmt.Sprintf("endpoint=%s %s", s3.Endpoint, detail)
		}
		return ServiceStatus{Name: "S3 Storage", Status: "ok", Detail: detail, CheckedAt: now}
	}
	path := h.cfg.Storage.Local.BasePath
	if _, err := os.Stat(path); err != nil {
		return ServiceStatus{Name: "Local Storage", Status: "error", Detail: path, CheckedAt: now}
	}
	detail := path
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err == nil {
		total := fs.Blocks * uint64(fs.Bsize)
		free := fs.Bavail * uint64(fs.Bsize)
		detail = fmt.Sprintf("%s · free %s / %s", path, fmtBytes(free), fmtBytes(total))
	}
	return ServiceStatus{Name: "Local Storage", Status: "ok", Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkLDAP(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.ldap.TestConnection(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	detail := fmt.Sprintf("%s:%d · base=%s · bind=%s", h.cfg.LDAP.Host, h.cfg.LDAP.Port, h.cfg.LDAP.SearchBase, h.cfg.LDAP.BindDN)
	if err != nil {
		return ServiceStatus{Name: "LDAP", Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: "LDAP", Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkOIDC(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.oidc.TestConnection(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	name := "OIDC"
	if h.cfg.OIDC.DisplayName != "" {
		name = "OIDC · " + h.cfg.OIDC.DisplayName
	}
	detail := fmt.Sprintf("issuer=%s · client=%s", h.cfg.OIDC.Issuer, h.cfg.OIDC.ClientID)
	if err != nil {
		return ServiceStatus{Name: name, Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: name, Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func disabled(name string) ServiceStatus {
	return ServiceStatus{Name: name, Status: "disabled", Detail: "not configured", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
}

// dsnDetail extracts host:port and dbname from a DSN without leaking credentials.
func dsnDetail(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			host := u.Host
			db := strings.TrimPrefix(u.Path, "/")
			return fmt.Sprintf("%s/%s", host, db)
		}
	}
	// keyword=value format
	var host, port, db string
	for _, part := range strings.Fields(dsn) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "host":
			host = kv[1]
		case "port":
			port = kv[1]
		case "dbname":
			db = kv[1]
		}
	}
	if host == "" {
		host = "localhost"
	}
	if port != "" {
		host = host + ":" + port
	}
	if db != "" {
		return host + "/" + db
	}
	return host
}

func fmtBytes(b uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/KB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
