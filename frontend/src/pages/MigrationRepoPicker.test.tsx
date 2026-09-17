import { useState } from 'react'
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MigrationRepoPicker, scopedRepoSelection, validateMigrationRepoScope, type PreviewRepo } from './MigrationRepoPicker'

const repos: PreviewRepo[] = [
  { name: 'raw-hosted', format: 'raw', type: 'hosted' },
  { name: 'maven-central', format: 'maven2', type: 'proxy' },
  { name: 'npm-hosted', format: 'npm', type: 'hosted' },
]

function Harness({ initial }: { initial: string[] }) {
  const [selected, setSelected] = useState(initial)
  return <MigrationRepoPicker repos={repos} selected={selected} onChange={setSelected} />
}

describe('MigrationRepoPicker', () => {
  it('filters, selects all visible, and None clears the whole selection', async () => {
    const user = userEvent.setup()
    render(<Harness initial={repos.map(r => r.name)} />)

    expect(screen.getByText('3 of 3 selected')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: /Filter repositories/ }), 'hosted')
    expect(screen.queryByRole('checkbox', { name: /maven-central/ })).not.toBeInTheDocument()
    expect(screen.getByText(/1 selected is hidden by the filter/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'None' }))
    expect(screen.getByText('0 of 3 selected')).toBeInTheDocument()

    await user.clear(screen.getByRole('textbox', { name: /Filter repositories/ }))
    expect(screen.getByRole('checkbox', { name: /maven-central/ })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: /raw-hosted/ })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: /npm-hosted/ })).not.toBeChecked()

    await user.click(screen.getByRole('button', { name: 'All' }))
    expect(screen.getByText('3 of 3 selected')).toBeInTheDocument()
  })

  it('shows an empty state when the filter matches nothing', async () => {
    const user = userEvent.setup()
    render(<Harness initial={repos.map(r => r.name)} />)
    await user.type(screen.getByRole('textbox', { name: /Filter repositories/ }), 'no-such-repo')
    expect(screen.getByText('No repositories match.')).toBeInTheDocument()
  })

  it('toggles a single repository', async () => {
    const user = userEvent.setup()
    render(<Harness initial={repos.map(r => r.name)} />)
    await user.click(screen.getByRole('checkbox', { name: /raw-hosted/ }))
    expect(screen.getByRole('checkbox', { name: /raw-hosted/ })).not.toBeChecked()
    expect(screen.getByText('2 of 3 selected')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: /raw-hosted/ }))
    expect(screen.getByRole('checkbox', { name: /raw-hosted/ })).toBeChecked()
  })

  it('warns that a selected group has no artifacts', async () => {
    const user = userEvent.setup()
    function GroupHarness() {
      const [selected, setSelected] = useState<string[]>(['raw-hosted'])
      return (
        <MigrationRepoPicker
          repos={[...repos, { name: 'raw-group', format: 'raw', type: 'group' }]}
          selected={selected}
          onChange={setSelected}
        />
      )
    }
    render(<GroupHarness />)
    expect(screen.queryByText(/Groups have no artifacts/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: /raw-group/ }))
    expect(screen.getByText(/selecting one also migrates its members/i)).toBeInTheDocument()
  })

  it('greys out proxy and group when only artifacts will run', async () => {
    const user = userEvent.setup()
    function HostedOnlyHarness() {
      const [selected, setSelected] = useState<string[]>([])
      return (
        <MigrationRepoPicker
          hostedOnly
          repos={[...repos, { name: 'raw-group', format: 'raw', type: 'group' }]}
          selected={selected}
          onChange={setSelected}
        />
      )
    }
    render(<HostedOnlyHarness />)
    expect(screen.getByText('0 of 2 hosted selected')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /maven-central/ })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: /raw-group/ })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: /raw-hosted/ })).toBeEnabled()
    expect(screen.getByText(/Artifacts copy only from hosted/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'All' }))
    expect(screen.getByText('2 of 2 hosted selected')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /raw-hosted/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /npm-hosted/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /maven-central/ })).not.toBeChecked()
  })
})

describe('scopedRepoSelection', () => {
  it('keeps every name unless artifacts-only', () => {
    expect(scopedRepoSelection(repos, ['maven-central', 'raw-hosted'], false)).toEqual(['maven-central', 'raw-hosted'])
    expect(scopedRepoSelection(repos, ['maven-central', 'raw-hosted'], true)).toEqual(['raw-hosted'])
  })
})

describe('validateMigrationRepoScope', () => {
  it('requires a tested connection when repositories or artifacts are on', () => {
    expect(validateMigrationRepoScope({
      migrateRepos: true, migrateBlobs: false, previewed: false, previewRepoCount: 0, selectedCount: 0,
    })).toMatch(/Test the connection/)
    expect(validateMigrationRepoScope({
      migrateRepos: false, migrateBlobs: false, previewed: false, previewRepoCount: 0, selectedCount: 0,
    })).toBeNull()
  })

  it('requires a selection only when the preview actually listed repositories', () => {
    expect(validateMigrationRepoScope({
      migrateRepos: true, migrateBlobs: true, previewed: true, previewRepoCount: 2, selectedCount: 0,
    })).toMatch(/Select at least one repository/)
    expect(validateMigrationRepoScope({
      migrateRepos: true, migrateBlobs: true, previewed: true, previewRepoCount: 0, selectedCount: 0,
    })).toBeNull()
    expect(validateMigrationRepoScope({
      migrateRepos: true, migrateBlobs: true, previewed: true, previewRepoCount: 2, selectedCount: 1,
    })).toBeNull()
  })

  it('rejects artifacts-only when the source has no hosted repository', () => {
    expect(validateMigrationRepoScope({
      migrateRepos: false, migrateBlobs: true, previewed: true, previewRepoCount: 0, selectedCount: 0,
    })).toMatch(/No hosted repositories/)
  })
})
