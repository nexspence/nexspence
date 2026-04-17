package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type AuditHandler struct {
	repo repository.AuditRepo
}

func NewAuditHandler(repo repository.AuditRepo) *AuditHandler {
	return &AuditHandler{repo: repo}
}

// List GET /service/rest/v1/audit
// Query params: domain, action, limit, offset
func (h *AuditHandler) List(c *gin.Context) {
	domainFilter := c.Query("domain")
	actionFilter := c.Query("action")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	events, err := h.repo.List(c.Request.Context(), domainFilter, actionFilter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []domain.AuditEvent{}
	}
	c.JSON(http.StatusOK, gin.H{"items": events})
}
