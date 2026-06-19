package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// taskCleanup is the minimal interface TasksHandler needs to list and run cleanup policies.
// In production this is satisfied by a small adapter combining the cleanup-policy repo's List
// with *service.CleanupService.RunPolicy (wired in router.go).
type taskCleanup interface {
	List(ctx context.Context) ([]domain.CleanupPolicy, error)
	RunPolicy(ctx context.Context, id string) error
}

// taskReplication is the minimal interface TasksHandler needs to list and run replication rules.
// Satisfied directly by *service.ReplicationService.
type taskReplication interface {
	ListRules(ctx context.Context) ([]domain.ReplicationRule, error)
	RunRule(ctx context.Context, id string) error
}

// TasksHandler exposes Nexus-compatible task endpoints backed by cleanup policies and
// replication rules.
type TasksHandler struct {
	cleanup taskCleanup
	repl    taskReplication
}

// NewTasksHandler constructs a TasksHandler from the cleanup and replication runners.
func NewTasksHandler(cleanup taskCleanup, repl taskReplication) *TasksHandler {
	return &TasksHandler{cleanup: cleanup, repl: repl}
}

// List GET /service/rest/v1/tasks — merge cleanup policies and replication rules into the
// Nexus task shape.
func (h *TasksHandler) List(c *gin.Context) {
	ctx := c.Request.Context()

	policies, err := h.cleanup.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rules, err := h.repl.ListRules(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items := []gin.H{}

	for _, p := range policies {
		lastRunResult := ""
		if p.LastRunAt != nil {
			lastRunResult = "OK"
		}
		items = append(items, gin.H{
			"id":            "cleanup:" + p.ID,
			"name":          p.Name,
			"type":          "repository.cleanup",
			"message":       "",
			"currentState":  "WAITING",
			"lastRunResult": lastRunResult,
			"nextRun":       nil,
			"lastRun":       formatLastRun(p.LastRunAt),
		})
	}

	for _, r := range rules {
		currentState := "WAITING"
		if r.LastRunStatus == "running" {
			currentState = "RUNNING"
		}
		lastRunResult := ""
		switch r.LastRunStatus {
		case "ok":
			lastRunResult = "OK"
		case "error":
			lastRunResult = "ERROR"
		}
		items = append(items, gin.H{
			"id":            "replication:" + r.ID,
			"name":          r.Name,
			"type":          "replication",
			"message":       "",
			"currentState":  currentState,
			"lastRunResult": lastRunResult,
			"nextRun":       nil,
			"lastRun":       formatLastRun(r.LastRunAt),
		})
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "continuationToken": nil})
}

// Run POST /service/rest/v1/tasks/:id/run — trigger a task by its prefixed id.
func (h *TasksHandler) Run(c *gin.Context) {
	id := c.Param("id")
	prefix, uuid, ok := strings.Cut(id, ":")
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	ctx := c.Request.Context()
	var err error
	switch prefix {
	case "cleanup":
		err = h.cleanup.RunPolicy(ctx, uuid)
	case "replication":
		err = h.repl.RunRule(ctx, uuid)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// formatLastRun returns the RFC3339 string for a non-nil time, or nil otherwise.
func formatLastRun(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}
