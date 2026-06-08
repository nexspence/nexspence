# ---- Local filesystem blob store (always created) ----------------------------
# This is where every repository below stores its artifacts.
resource "nexspence_blobstore" "main" {
  name = "tf-main"
  type = "local"
  path = "./data/blobs/tf-main"

  # Optional soft cap on the store (bytes). Uncomment to enforce (10 GiB shown).
  # quota_bytes = 10 * 1024 * 1024 * 1024
}

# ---- Optional S3 / MinIO blob store ------------------------------------------
# Toggle with `enable_s3_blobstore = true` once you have a reachable endpoint.
resource "nexspence_blobstore" "s3" {
  count = var.enable_s3_blobstore ? 1 : 0

  name = "tf-s3"
  type = "s3"

  s3 = {
    bucket           = var.s3_bucket
    region           = var.s3_region
    endpoint         = var.s3_endpoint
    access_key       = var.s3_access_key
    secret_key       = var.s3_secret_key
    force_path_style = true # required for MinIO / path-style endpoints
  }
}
