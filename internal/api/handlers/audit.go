package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	pgaudit "github.com/nexspence-oss/nexspence/internal/repository/postgres"
)

type AuditHandler struct {
	repo repository.AuditRepo
}

func NewAuditHandler(repo repository.AuditRepo) *AuditHandler {
	return &AuditHandler{repo: repo}
}

// parseAuditQuery extracts AuditQuery from query string.
// Returns (q, error). On error, the caller should return 400.
func parseAuditQuery(c *gin.Context) (repository.AuditQuery, error) {
	q := repository.AuditQuery{
		Domain:   c.Query("domain"),
		Action:   c.Query("action"),
		Username: c.Query("username"),
	}
	if v := c.Query("from"); v != "" {
		t, err := parseDate(v)
		if err != nil {
			return q, fmt.Errorf("invalid 'from' value: %w", err)
		}
		q.From = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := parseDate(v)
		if err != nil {
			return q, fmt.Errorf("invalid 'to' value: %w", err)
		}
		// AuditQuery.To is exclusive. A bare YYYY-MM-DD from the UI is parsed
		// to 00:00 UTC of that day, but users mean "include this day too" —
		// bump by 24h so `to=2026-04-24` matches events through 23:59:59 UTC
		// on April 24. For RFC3339 inputs we trust the caller's intent.
		if isBareDate(v) {
			t = t.Add(24 * time.Hour)
		}
		q.To = &t
	}
	q.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "100"))
	q.Offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	return q, nil
}

// parseDate accepts either an ISO date (2026-04-01) or RFC3339 (2026-04-01T12:00:00Z).
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// isBareDate reports whether s parses as YYYY-MM-DD (no time component).
func isBareDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil && len(s) == 10
}

// List GET /service/rest/v1/audit
//
//	- format=ndjson  → streaming NDJSON download
//	- otherwise      → {"items":[...], "total": N}
//
// Query params: domain, action, username, from, to, limit, offset, format.
func (h *AuditHandler) List(c *gin.Context) {
	q, err := parseAuditQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if c.Query("format") == "ndjson" {
		h.exportNDJSON(c, q)
		return
	}

	items, total, err := h.repo.List(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.AuditEvent{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (h *AuditHandler) exportNDJSON(c *gin.Context, q repository.AuditQuery) {
	filename := "audit-" + time.Now().UTC().Format("2006-01-02") + ".ndjson"
	c.Writer.Header().Set("Content-Type", "application/x-ndjson")
	c.Writer.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Writer.WriteHeader(http.StatusOK)

	enc := newJSONLineEncoder(c.Writer)
	streamErr := h.repo.Stream(c.Request.Context(), q, func(e domain.AuditEvent) error {
		return enc.encode(e)
	})

	if streamErr == nil {
		return
	}
	if errors.Is(streamErr, pgaudit.ErrStreamCapExceeded) {
		// Headers already sent — best we can do is append an error line so the
		// client sees the failure rather than a silently truncated download.
		_ = enc.encode(map[string]any{
			"error": "row cap exceeded; narrow date range and retry",
			"cap":   100_000,
		})
		return
	}
	// Generic streaming failure — same fallback.
	_ = enc.encode(map[string]any{"error": streamErr.Error()})
}
