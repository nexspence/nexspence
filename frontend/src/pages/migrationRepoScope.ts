// Repository-scope helpers for the migration pickers, kept out of
// MigrationRepoPicker.tsx so that module exports only components (fast refresh).

export interface PreviewRepo {
  name: string
  format: string
  type: string
}

export function isHostedRepo(r: PreviewRepo) {
  return r.type === 'hosted'
}

/** Hosted packages and proxy caches; groups have none of their own. */
export function isBlobSourceRepo(r: PreviewRepo) {
  return isHostedRepo(r) || r.type === 'proxy'
}

/** Drop names the current scope cannot use (groups when only artifacts run). */
export function scopedRepoSelection(repos: PreviewRepo[], selected: string[], blobSourcesOnly: boolean) {
  if (!blobSourcesOnly) return selected
  const keep = new Set(repos.filter(isBlobSourceRepo).map(r => r.name))
  return selected.filter(n => keep.has(n))
}

/** Block a Repositories/Artifacts job that would otherwise copy the whole instance. */
export function validateMigrationRepoScope(opts: {
  migrateRepos: boolean
  migrateBlobs: boolean
  previewed: boolean
  previewRepoCount: number
  selectedCount: number
}): string | null {
  if (!opts.migrateRepos && !opts.migrateBlobs) return null
  if (!opts.previewed) {
    return 'Test the connection to pick repositories, or uncheck Repositories and Artifacts'
  }
  if (opts.migrateBlobs && !opts.migrateRepos && opts.previewRepoCount === 0) {
    return 'No hosted or proxy repositories to copy artifacts from'
  }
  if (opts.previewRepoCount > 0 && opts.selectedCount === 0) {
    return 'Select at least one repository, or uncheck Repositories and Artifacts'
  }
  return null
}
