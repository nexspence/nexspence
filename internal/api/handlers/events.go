package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/events"
)

// EventsHandler streams realtime broker events to clients over Server-Sent Events.
type EventsHandler struct {
	broker *events.Broker
	// keepAlive is how often to send a comment heartbeat to detect disconnects
	// and keep proxies from closing idle connections. Defaults to 15s.
	keepAlive time.Duration
}

// NewEventsHandler constructs an EventsHandler that streams from the given broker with a 15s heartbeat.
func NewEventsHandler(broker *events.Broker) *EventsHandler {
	return &EventsHandler{broker: broker, keepAlive: 15 * time.Second}
}

// Stream handles GET /api/v1/events.
//
// Query params:
//
//	event=<name>     repeatable; if any are present, only those event types are streamed
//
// EventSource (browser) cannot set Authorization headers on the WebSocket-style
// handshake — auth is therefore performed by the surrounding middleware
// (which also accepts ?token=... when wired that way) before this handler runs.
func (h *EventsHandler) Stream(c *gin.Context) {
	if h.broker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "events broker not configured"})
		return
	}

	wantedRaw := c.Request.URL.Query()["event"]
	wanted := map[domain.WebhookEvent]bool{}
	for _, e := range wantedRaw {
		if e != "" {
			wanted[domain.WebhookEvent(e)] = true
		}
	}
	filter := func(ev domain.WebhookEvent) bool {
		if len(wanted) == 0 {
			return true
		}
		return wanted[ev]
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}
	if _, err := io.WriteString(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	sub := h.broker.Subscribe()
	defer h.broker.Unsubscribe(sub)

	ticker := time.NewTicker(h.keepAlive)
	defer ticker.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := io.WriteString(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case payload, ok := <-sub.C:
			if !ok {
				return
			}
			if !filter(payload.Event) {
				continue
			}
			body, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(c.Writer, "event: "+string(payload.Event)+"\ndata: "); err != nil {
				return
			}
			if _, err := c.Writer.Write(body); err != nil {
				return
			}
			if _, err := io.WriteString(c.Writer, "\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
