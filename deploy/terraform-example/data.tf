# =============================================================================
# Data sources — both that the provider exposes.
# =============================================================================

# Look up a single repository by name (here, one we just created).
data "nexspence_repository" "maven_group" {
  name       = nexspence_repository.group["maven2"].name
  depends_on = [nexspence_repository.group]
}

# List every repository the server knows about.
data "nexspence_repositories" "all" {
  depends_on = [
    nexspence_repository.hosted,
    nexspence_repository.proxy,
    nexspence_repository.group,
    nexspence_repository.raw_quota_demo,
    nexspence_repository.docker_quota_demo,
    nexspence_repository.maven_releases,
    nexspence_repository.npm_releases,
  ]
}
