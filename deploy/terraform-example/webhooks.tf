# =============================================================================
# Webhooks (nexspence_webhook) — POST repository/artifact events to a URL.
# The secret is write-only (the API never returns it).
# =============================================================================

resource "nexspence_webhook" "ci_pipeline" {
  name   = "ci-pipeline"
  url    = "https://ci.example.com/hooks/nexspence"
  secret = var.webhook_secret
  events = ["artifact.published", "artifact.deleted"]
  active = true
}

resource "nexspence_webhook" "audit_log" {
  name   = "audit-log"
  url    = "https://logs.example.com/ingest/nexspence"
  secret = var.webhook_secret
  events = ["repo.created", "repo.updated", "repo.deleted"]
  active = false # created disabled; flip to true to start delivery
}
