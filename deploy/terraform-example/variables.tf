variable "nexspence_url" {
  type        = string
  description = "Base URL of the locally deployed Nexspence stack."
  default     = "http://localhost:8080"
}

variable "nexspence_username" {
  type        = string
  description = "Admin username (bootstrap admin from config.yaml)."
  default     = "admin"
}

variable "nexspence_password" {
  type        = string
  description = "Admin password (bootstrap admin from config.yaml)."
  default     = "admin123"
  sensitive   = true
}

variable "nexspence_token" {
  type        = string
  description = "Optional nxs_* API token. Overrides username/password when set."
  default     = ""
  sensitive   = true
}

# ---- Demo user passwords (write-only; the API never returns them) -------------

variable "alice_password" {
  type        = string
  description = "Password for the demo user 'alice'."
  default     = "alice-Chang3Me!"
  sensitive   = true
}

variable "bob_password" {
  type        = string
  description = "Password for the demo user 'bob'."
  default     = "bob-Chang3Me!"
  sensitive   = true
}

# ---- Optional S3 blob store (MinIO / Ceph / AWS) -----------------------------

variable "enable_s3_blobstore" {
  type        = bool
  description = "Create the example S3 blob store. Requires a reachable S3/MinIO endpoint."
  default     = false
}

variable "s3_bucket" {
  type        = string
  description = "Bucket name for the S3 blob store (must already exist). The docker-compose MinIO pre-creates nexspence-blobs, nexspence-s3-1, nexspence-s3-2."
  default     = "nexspence-s3-1"
}

variable "s3_endpoint" {
  type        = string
  description = <<-EOT
    Custom S3 endpoint. IMPORTANT: this URL is dialed by the Nexspence SERVER, not by Terraform.
    - Server runs in Docker Compose (default here)      -> http://minio:9000  (in-network hostname)
    - Server runs on the host (go run) + MinIO published -> http://localhost:9000
    Leave empty for real AWS S3.
  EOT
  default     = "http://minio:9000"
}

variable "s3_region" {
  type    = string
  default = "us-east-1"
}

variable "s3_access_key" {
  type      = string
  default   = "minioadmin"
  sensitive = true
}

variable "s3_secret_key" {
  type      = string
  default   = "minioadmin"
  sensitive = true
}
