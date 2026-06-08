# =============================================================================
# Cleanup policies (nexspence_cleanup_policy) — delete stale assets on a schedule.
# Attach them to repositories via the repository's cleanup_policy_ids.
# =============================================================================

resource "nexspence_cleanup_policy" "stale_snapshots" {
  name                 = "stale-snapshots"
  description          = "Drop Maven snapshots unused for 14d and older than 30d"
  format               = "maven2"
  artifact_age_days    = 30
  last_downloaded_days = 14
  criteria_path_prefix = "/"
  schedule_cron        = "0 3 * * *"
}

resource "nexspence_cleanup_policy" "keep_recent" {
  name              = "keep-5-versions"
  description       = "Across all formats, keep the 5 newest versions"
  format            = "*"
  retain_n_versions = 5
  artifact_age_days = 90
  enabled           = true
}
