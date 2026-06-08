# =============================================================================
# Build promotion (nexspence_promotion_rule) — copy components from a source
# repo to a target repo, optionally gated on a CEL path filter, a passing scan,
# and/or manual approval. Source repos here are the format hosted repos above.
# =============================================================================

resource "nexspence_repository" "maven_releases" {
  name        = "maven-releases"
  format      = "maven2"
  type        = "hosted"
  blob_store  = nexspence_blobstore.main.name
  description = "Promotion target for vetted Maven artifacts"
}

resource "nexspence_repository" "npm_releases" {
  name        = "npm-releases"
  format      = "npm"
  type        = "hosted"
  blob_store  = nexspence_blobstore.main.name
  description = "Promotion target for vetted npm packages"
}

# Auto-promote com.acme Maven artifacts once they pass a security scan.
resource "nexspence_promotion_rule" "maven_strict" {
  name              = "maven-staging-to-releases"
  from_repo         = nexspence_repository.hosted["maven2"].name
  to_repo           = nexspence_repository.maven_releases.name
  path_filter       = "path.startsWith(\"/com/acme/\")"
  require_scan_pass = true
}

# Promote npm packages only after an admin approves.
resource "nexspence_promotion_rule" "npm_manual" {
  name                    = "npm-staging-to-releases"
  from_repo               = nexspence_repository.hosted["npm"].name
  to_repo                 = nexspence_repository.npm_releases.name
  require_manual_approval = true
}
