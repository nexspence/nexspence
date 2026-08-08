/** A Nexus → Nexspence migration job, as the API reports it. */
export interface MigrationJob {
  id: string
  status: 'pending' | 'running' | 'paused' | 'done' | 'error'
  sourceUrl: string
  repositoriesTotal: number
  repositoriesDone: number
  assetsTotal: number
  assetsDone: number
  errorCount: number
  lastError?: string
  createdAt: string
  updatedAt: string
}

/**
 * How often to re-read the job list, or false to stop.
 *
 * A pending job counts as live: it is one the runner has picked up but not yet
 * reported on, and it is exactly the state that used to sit there forever.
 * A paused job is parked by an operator and changes nothing on its own.
 */
export function shouldPollJobs(jobs?: { status: string }[]): number | false {
  const live = jobs?.some(j => j.status === 'pending' || j.status === 'running')
  return live ? 3000 : false
}
