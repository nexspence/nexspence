# =============================================================================
# Repositories — every supported format × every repository type
#
# The provider supports 14 formats and 3 types (hosted / proxy / group).
# For each format we create:
#   <format>-hosted   a hosted repo  (you publish artifacts here)
#   <format>-proxy    a proxy repo   (read-through cache of a public upstream)
#   <format>-group    a group repo   (aggregates hosted + proxy under one URL,
#                                      writes routed to the hosted member)
# => 14 × 3 = 42 repositories.
# =============================================================================

locals {
  # format => public upstream the proxy caches.
  formats = {
    maven2    = "https://repo1.maven.org/maven2/"
    npm       = "https://registry.npmjs.org/"
    pypi      = "https://pypi.org/"
    docker    = "https://registry-1.docker.io/"
    go        = "https://proxy.golang.org/"
    nuget     = "https://api.nuget.org/v3/index.json"
    raw       = "https://github.com/"
    apt       = "http://deb.debian.org/debian/"
    yum       = "https://download.fedoraproject.org/pub/fedora/linux/releases/"
    helm      = "https://charts.helm.sh/stable/"
    cargo     = "https://index.crates.io/"
    conan     = "https://center.conan.io/"
    conda     = "https://repo.anaconda.com/pkgs/main/"
    terraform = "https://registry.terraform.io/"
  }
}

# ---- Hosted: local storage for artifacts you publish -------------------------
resource "nexspence_repository" "hosted" {
  for_each = local.formats

  name            = "${each.key}-hosted"
  format          = each.key
  type            = "hosted"
  blob_store      = nexspence_blobstore.main.name
  description     = "${each.key} hosted repository (managed by Terraform)"
  online          = true
  allow_anonymous = false
}

# ---- Proxy: read-through cache of a public upstream --------------------------
resource "nexspence_repository" "proxy" {
  for_each = local.formats

  name            = "${each.key}-proxy"
  format          = each.key
  type            = "proxy"
  blob_store      = nexspence_blobstore.main.name
  description     = "${each.key} proxy of ${each.value}"
  online          = true
  allow_anonymous = true

  proxy {
    remote_url = each.value
  }
}

# ---- Group: one URL aggregating hosted + proxy ------------------------------
resource "nexspence_repository" "group" {
  for_each = local.formats

  name            = "${each.key}-group"
  format          = each.key
  type            = "group"
  blob_store      = nexspence_blobstore.main.name
  description     = "${each.key} group (hosted + proxy)"
  online          = true
  allow_anonymous = true

  group {
    member_names = [
      nexspence_repository.hosted[each.key].name,
      nexspence_repository.proxy[each.key].name,
    ]
    # PUT/POST routed to the hosted member (group writes, Phase 51).
    writable_member = nexspence_repository.hosted[each.key].name
  }
}

# ---- Example: per-repo quota + cleanup policy attachment ---------------------
# quota_bytes and cleanup_policy_ids are also supported. Cleanup policies are not
# managed by this provider, so attach existing policy UUIDs if you have them.
resource "nexspence_repository" "raw_quota_demo" {
  name        = "raw-quota-demo"
  format      = "raw"
  type        = "hosted"
  blob_store  = nexspence_blobstore.main.name
  description = "Raw hosted repo with a 1 GiB quota"
  quota_bytes = 1 * 1024 * 1024 * 1024

  # cleanup_policy_ids = ["00000000-0000-0000-0000-000000000000"]
}
