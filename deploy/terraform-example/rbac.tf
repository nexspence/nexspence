# =============================================================================
# RBAC — content selectors -> privileges -> roles -> users
#
# Nexspence collapses privilege creation into a single content-selector-scoped
# type (repository-content-selector). CEL variables available in an expression:
# format, path, repository.
# =============================================================================

# ---- Content selectors (CEL) -------------------------------------------------
resource "nexspence_content_selector" "team_a" {
  name        = "team-a"
  description = "Everything under com.acme for team A"
  expression  = "path.startsWith(\"/com/acme/\")"
}

resource "nexspence_content_selector" "npm_scope" {
  name        = "npm-acme-scope"
  description = "The @acme npm scope"
  expression  = "format == \"npm\" && path.startsWith(\"/@acme/\")"
}

resource "nexspence_content_selector" "docker_all" {
  name        = "docker-all"
  description = "All docker content"
  expression  = "format == \"docker\""
}

# ---- Privileges (each scoped to one content selector) ------------------------
resource "nexspence_privilege" "team_a_rw" {
  name             = "team-a-rw"
  description      = "Read/write access to team-a content"
  content_selector = nexspence_content_selector.team_a.name
}

resource "nexspence_privilege" "npm_scope_rw" {
  name             = "npm-acme-scope-rw"
  description      = "Read/write access to the @acme npm scope"
  content_selector = nexspence_content_selector.npm_scope.name
}

resource "nexspence_privilege" "docker_ro" {
  name             = "docker-all-ro"
  description      = "Access to all docker content"
  content_selector = nexspence_content_selector.docker_all.name
}

# ---- Roles (group privileges) ------------------------------------------------
resource "nexspence_role" "team_a_dev" {
  name        = "team-a-dev"
  description = "Team A developers"
  privileges = [
    nexspence_privilege.team_a_rw.name,
    nexspence_privilege.npm_scope_rw.name,
  ]
}

resource "nexspence_role" "docker_user" {
  name        = "docker-user"
  description = "Can use docker repositories"
  privileges  = [nexspence_privilege.docker_ro.name]
}

# ---- Users -------------------------------------------------------------------
resource "nexspence_user" "alice" {
  username   = "alice"
  password   = var.alice_password
  email      = "alice@example.com"
  first_name = "Alice"
  last_name  = "Acme"
  status     = "active"
  roles = [
    nexspence_role.team_a_dev.name,
    nexspence_role.docker_user.name,
  ]
}

resource "nexspence_user" "bob" {
  username   = "bob"
  password   = var.bob_password
  email      = "bob@example.com"
  first_name = "Bob"
  last_name  = "Builder"
  status     = "disabled" # demonstrates the status field
  roles      = [nexspence_role.docker_user.name]
}
