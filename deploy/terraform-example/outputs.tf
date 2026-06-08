output "blob_stores" {
  description = "Created blob store names."
  value = compact([
    nexspence_blobstore.main.name,
    var.enable_s3_blobstore ? nexspence_blobstore.s3[0].name : "",
  ])
}

output "hosted_repository_urls" {
  description = "URL of each hosted repository (computed by the server)."
  value       = { for k, r in nexspence_repository.hosted : k => r.url }
}

output "group_repository_urls" {
  description = "URL of each group repository — point your clients here."
  value       = { for k, r in nexspence_repository.group : k => r.url }
}

output "maven_group_lookup" {
  description = "Data-source lookup of the maven group repository."
  value = {
    name   = data.nexspence_repository.maven_group.name
    format = data.nexspence_repository.maven_group.format
    type   = data.nexspence_repository.maven_group.type
    url    = data.nexspence_repository.maven_group.url
  }
}

output "total_repositories_on_server" {
  description = "How many repositories the server reports (via the list data source)."
  value       = length(data.nexspence_repositories.all.repositories)
}

output "created_users" {
  description = "Demo users created."
  value       = [nexspence_user.alice.username, nexspence_user.bob.username]
}
