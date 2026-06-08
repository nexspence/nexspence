provider "nexspence" {
  url = var.nexspence_url

  # Auth option A — basic auth (the bootstrap admin from config.yaml).
  username = var.nexspence_username
  password = var.nexspence_password

  # Auth option B — nxs_* API token (preferred). If set it takes precedence.
  # Leave username/password empty when using a token.
  # token = var.nexspence_token
}
