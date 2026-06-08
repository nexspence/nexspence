# =============================================================================
# Routing rules (nexspence_routing_rule) — ALLOW/BLOCK request paths by regex.
# Attaching a rule to a repository is done in the UI / API (not a provider field).
# =============================================================================

resource "nexspence_routing_rule" "block_snapshots" {
  name        = "block-snapshots"
  description = "Deny any path containing -SNAPSHOT"
  mode        = "BLOCK"
  matchers    = [".*-SNAPSHOT.*"]
}

resource "nexspence_routing_rule" "allow_acme_only" {
  name        = "allow-acme-only"
  description = "Only allow the com.acme namespace through"
  mode        = "ALLOW"
  matchers    = ["^/com/acme/.*"]
}
