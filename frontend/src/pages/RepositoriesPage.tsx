import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Plus, Trash2, RefreshCw, Settings2 } from 'lucide-react'
import { nexusApi, nexspenceApi, apiClient } from '@/api/client'
import { useAuthStore } from '@/store/authStore'
import styles from './RepositoriesPage.module.css'
import { Select } from '../components/Select'

interface Repository {
  id: string
  name: string
  format: string
  type: string
  online: boolean
  allowAnonymous: boolean
  description?: string
  cleanupPolicyIds?: string[]
  quotaBytes?: number | null
  blobStoreId?: string | null
}

interface BlobStoreLite {
  id: string
  name: string
  type: string
  quotaBytes?: number | null
  usedBytes?: number
}

interface CleanupPolicyRow {
  id: string
  name: string
  format: string
}

function cleanupPoliciesForFormat(policies: CleanupPolicyRow[], format: string) {
  return policies.filter(p => p.format === '*' || p.format === format)
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316',
  npm:    '#ef4444',
  docker: '#3b82f6',
  pypi:   '#a78bfa',
  go:     '#06b6d4',
  nuget:  '#8b5cf6',
  helm:   '#0ea5e9',
  raw:    '#6b7280',
  apt:    '#f59e0b',
  yum:    '#10b981',
}

const TYPE_LABELS: Record<string, string> = {
  hosted: 'Hosted',
  proxy:  'Proxy',
  group:  'Group',
}

export default function RepositoriesPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const isAdmin = useAuthStore(s => s.isAdmin())
  const [filter, setFilter] = useState('')
  const [formatFilter, setFormatFilter] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [editRepo, setEditRepo] = useState<Repository | null>(null)

  const { data: repos = [], isLoading, isError, error, refetch } = useQuery<Repository[]>({
    queryKey: ['repositories', formatFilter],
    queryFn: () =>
      nexusApi.listRepositories(formatFilter ? { format: formatFilter } : {})
        .then(r => r.data),
  })

  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })
  const storeNameById = new Map(blobStores.map(b => [b.id, b.name]))

  const deleteMutation = useMutation({
    mutationFn: (name: string) => nexusApi.deleteRepository(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['repositories'] }),
  })

  const filtered = repos.filter(r =>
    r.name.toLowerCase().includes(filter.toLowerCase()) ||
    (r.description ?? '').toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>Repositories</h1>
          <p className={styles.subtitle}>{repos.length} total</p>
        </div>
        <div className={styles.actions}>
          <button className={styles.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={16} />
          </button>
          {isAdmin && (
            <button className={styles.createBtn} onClick={() => setShowCreate(true)}>
              <Plus size={16} />
              Create Repository
            </button>
          )}
        </div>
      </div>

      <div className={styles.toolbar}>
        <input
          className={styles.search}
          placeholder="Filter by name…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
        <Select
          options={[
            { value: '', label: 'All formats' },
            ...['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => ({ value: f, label: f })),
          ]}
          value={formatFilter}
          onChange={setFormatFilter}
          style={{ minWidth: 140 }}
        />
      </div>

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : isError ? (
        <div className={styles.empty}>
          <Database size={40} className={styles.emptyIcon} />
          <p style={{ color: '#ef4444', marginBottom: 8 }}>Error loading repositories</p>
          <p style={{ fontSize: 13, color: 'rgba(229,231,235,0.5)', marginBottom: 16 }}>
            {error instanceof Error ? error.message : 'Unable to access repositories. Check your permissions or contact your administrator.'}
          </p>
          <button className={styles.createBtn} onClick={() => refetch()} style={{ marginTop: 8 }}>
            <RefreshCw size={16} /> Retry
          </button>
        </div>
      ) : repos.length === 0 ? (
        <div className={styles.empty}>
          <Database size={40} className={styles.emptyIcon} />
          <p>No repositories found</p>
          <p style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginTop: 8 }}>
            You don't have access to any repositories. Contact your administrator to grant you access.
          </p>
          {!filter && isAdmin && (
            <button className={styles.createBtn} onClick={() => setShowCreate(true)}>
              <Plus size={16} /> Create your first repository
            </button>
          )}
        </div>
      ) : (
        <div className={styles.grid}>
          {filtered.map(repo => (
            <RepoCard
              key={repo.id}
              repo={repo}
              isAdmin={isAdmin}
              storeName={repo.blobStoreId ? storeNameById.get(repo.blobStoreId) : undefined}
              onClick={() => navigate(`/browse?repo=${repo.name}`)}
              onEdit={() => setEditRepo(repo)}
              onDelete={() => {
                if (confirm(`Delete repository "${repo.name}"?`)) {
                  deleteMutation.mutate(repo.name)
                }
              }}
            />
          ))}
        </div>
      )}

      {showCreate && (
        <CreateRepoModal
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            qc.invalidateQueries({ queryKey: ['repositories'] })
          }}
        />
      )}

      {editRepo && (
        <EditRepoModal
          key={editRepo.id}
          repo={editRepo}
          onClose={() => setEditRepo(null)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ['repositories'] })
            setEditRepo(null)
          }}
        />
      )}
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function RepoCard({
  repo,
  isAdmin,
  storeName,
  onClick,
  onEdit,
  onDelete,
}: {
  repo: Repository
  isAdmin: boolean
  storeName?: string
  onClick?: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const color = FORMAT_COLORS[repo.format] ?? '#6b7280'

  const { data: quota } = useQuery({
    queryKey: ['repoQuota', repo.name],
    queryFn: () => nexspenceApi.getRepositoryQuota(repo.name).then(r => r.data),
    staleTime: 30_000,
  })

  const pct = quota?.percentUsed ?? null
  const quotaColor = pct == null ? '#3b82f6' : pct >= 90 ? '#ef4444' : pct >= 70 ? '#f59e0b' : '#22c55e'

  return (
    <div className={styles.card} onClick={onClick} style={{ cursor: 'pointer' }}>
      <div className={styles.cardHeader}>
        <span className={styles.formatBadge} style={{ background: color + '22', color }}>
          {repo.format}
        </span>
        <span className={`${styles.typeBadge} ${styles['type_' + repo.type]}`}>
          {TYPE_LABELS[repo.type] ?? repo.type}
        </span>
        <span className={`${styles.statusDot} ${repo.online ? styles.online : styles.offline}`} />
        {repo.allowAnonymous && (
          <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 4, background: '#06b6d422', color: '#06b6d4', fontWeight: 600 }}>anon</span>
        )}
      </div>
      <div className={styles.cardName}>{repo.name}</div>
      {repo.description && (
        <div className={styles.cardDesc}>{repo.description}</div>
      )}
      {repo.type !== 'group' && storeName && (
        <div style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)', marginTop: 4 }}>
          on <span style={{ color: 'rgba(147,197,253,0.7)', fontWeight: 500 }}>{storeName}</span>
        </div>
      )}
      {quota != null && (
        <div className={styles.quotaBar}>
          <div className={styles.quotaText}>
            <span>{formatBytes(quota.usedBytes)} used</span>
            {quota.quotaBytes != null && (
              <span>/ {formatBytes(quota.quotaBytes)}</span>
            )}
          </div>
          {quota.quotaBytes != null && (
            <div className={styles.quotaTrack}>
              <div
                className={styles.quotaFill}
                style={{
                  width: `${Math.min(pct ?? 0, 100)}%`,
                  background: quotaColor,
                }}
              />
            </div>
          )}
        </div>
      )}
      {isAdmin && (
        <div className={styles.cardFooter}>
          <button type="button" className={styles.settingsBtn} onClick={e => { e.stopPropagation(); onEdit() }} title="Settings">
            <Settings2 size={14} />
          </button>
          <button type="button" className={styles.deleteBtn} onClick={e => { e.stopPropagation(); onDelete() }} title="Delete">
            <Trash2 size={14} />
          </button>
        </div>
      )}
    </div>
  )
}

// Default remote URLs per format for proxy repos
const PROXY_DEFAULTS: Record<string, string> = {
  maven2:  'https://repo1.maven.org/maven2/',
  npm:     'https://registry.npmjs.org/',
  pypi:    'https://pypi.org/',
  go:      'https://proxy.golang.org/',
  docker:  'https://registry-1.docker.io/',
  helm:    'https://charts.bitnami.com/bitnami/',
  nuget:   'https://api.nuget.org/v3/',
  cargo:   'https://index.crates.io/',
  apt:     'http://archive.ubuntu.com/ubuntu/',
  yum:     'https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/',
  raw:     '',
}

function CreateRepoModal({ onClose, onCreated }: {
  onClose: () => void
  onCreated: () => void
}) {
  const { data: allRepos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories({}).then(r => r.data),
  })

  const { data: cleanupPolicies = [] } = useQuery<CleanupPolicyRow[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })

  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })

  const defaultStoreId = blobStores.find(b => b.name === 'default')?.id ?? blobStores[0]?.id ?? ''

  const [form, setForm] = useState({
    name: '', format: 'maven2', type: 'hosted', description: '',
    remoteUrl: PROXY_DEFAULTS['maven2'],
    memberNames: [] as string[],
    cleanupPolicyIds: [] as string[],
    quotaGB: '',
    allowAnonymous: false,
    blobStoreId: '',
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const setField = (field: string, value: unknown) =>
    setForm(f => ({ ...f, [field]: value }))

  // When format changes on proxy, suggest the default URL
  const handleFormatChange = (fmt: string) => {
    setForm(f => ({
      ...f,
      format: fmt,
      remoteUrl: f.type === 'proxy' ? (PROXY_DEFAULTS[fmt] ?? '') : f.remoteUrl,
      cleanupPolicyIds: [],
    }))
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (form.type === 'proxy' && !form.remoteUrl.trim()) {
      setError('Remote URL is required for proxy repositories')
      return
    }
    if (form.type === 'group' && form.memberNames.length === 0) {
      setError('Select at least one member repository')
      return
    }
    const effectiveStoreId = form.type === 'group' ? '' : (form.blobStoreId || defaultStoreId)
    if (form.type !== 'group' && !effectiveStoreId) {
      setError('Select a blob store')
      return
    }

    // Enforce repo quota <= blob store quota (backend also checks).
    const quotaValue = form.quotaGB.trim() !== '' ? parseFloat(form.quotaGB) : NaN
    if (!isNaN(quotaValue) && quotaValue > 0 && effectiveStoreId) {
      const store = blobStores.find(b => b.id === effectiveStoreId)
      if (store?.quotaBytes != null) {
        const repoBytes = Math.round(quotaValue * 1024 * 1024 * 1024)
        if (repoBytes > store.quotaBytes) {
          setError(`Repository quota (${formatBytes(repoBytes)}) exceeds blob store "${store.name}" quota (${formatBytes(store.quotaBytes)})`)
          return
        }
      }
    }

    setLoading(true)
    try {
      const body: Record<string, unknown> = {
        name: form.name,
        description: form.description,
      }
      if (effectiveStoreId) {
        body.blobStoreId = effectiveStoreId
      }
      if (form.type === 'proxy') {
        body.proxyConfig = { remote_url: form.remoteUrl.trim() }
      }
      if (form.type === 'group') {
        body.formatConfig = { member_names: form.memberNames }
      }
      if (form.type !== 'group' && form.cleanupPolicyIds.length > 0) {
        body.cleanupPolicyIds = form.cleanupPolicyIds
      }
      if (form.quotaGB.trim() !== '') {
        const gb = parseFloat(form.quotaGB)
        if (!isNaN(gb) && gb > 0) {
          body.quotaBytes = Math.round(gb * 1024 * 1024 * 1024)
        }
      }
      body.allowAnonymous = form.allowAnonymous

      await apiClient.post(
        `/service/rest/v1/repositories/${form.format}/${form.type}`,
        body,
      )
      onCreated()
    } catch (err: any) {
      setError(err.response?.data?.error ?? 'Failed to create repository')
    } finally {
      setLoading(false)
    }
  }

  // Repos that can be members of a group (same format, not a group itself)
  const memberCandidates = allRepos.filter(
    r => r.format === form.format && r.type !== 'group'
  )

  const toggleMember = (name: string) =>
    setField('memberNames',
      form.memberNames.includes(name)
        ? form.memberNames.filter(n => n !== name)
        : [...form.memberNames, name]
    )

  const applicableCreate = cleanupPoliciesForFormat(cleanupPolicies, form.format)

  return (
    <div className={styles.modalOverlay} onClick={onClose}>
      <div className={styles.modal} onClick={e => e.stopPropagation()}>
        <h2 className={styles.modalTitle}>Create Repository</h2>
        <form onSubmit={handleSubmit} className={styles.form}>

          <div className={styles.formRow}>
            <label className={styles.label}>Name *</label>
            <input
              className={styles.input}
              value={form.name}
              onChange={e => setField('name', e.target.value)}
              required
              placeholder="my-repo"
            />
          </div>

          <div className={styles.formRow}>
            <label className={styles.label}>Format</label>
            <Select
              options={['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum','cargo','conan'].map(f => ({ value: f, label: f }))}
              value={form.format}
              onChange={handleFormatChange}
            />
          </div>

          <div className={styles.formRow}>
            <label className={styles.label}>Type</label>
            <Select
              options={[
                { value: 'hosted', label: 'Hosted — store artifacts locally' },
                { value: 'proxy',  label: 'Proxy — cache from remote registry' },
                { value: 'group',  label: 'Group — combine multiple repos' },
              ]}
              value={form.type}
              onChange={t => setForm(f => ({
                ...f,
                type: t,
                remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
              }))}
            />
          </div>

          {/* Proxy: Remote URL */}
          {form.type === 'proxy' && (
            <div className={styles.formRow}>
              <label className={styles.label}>Remote URL *</label>
              <input
                className={styles.input}
                type="url"
                value={form.remoteUrl}
                onChange={e => setField('remoteUrl', e.target.value)}
                required
                placeholder="https://registry.example.com/"
              />
              <span className={styles.hint}>
                URL of the upstream registry to proxy and cache
              </span>
            </div>
          )}

          {/* Group: member repos */}
          {form.type === 'group' && (
            <div className={styles.formRow}>
              <label className={styles.label}>Member Repositories *</label>
              {memberCandidates.length === 0 ? (
                <p className={styles.hint}>
                  No {form.format} hosted/proxy repos found. Create them first.
                </p>
              ) : (
                <div className={styles.memberList}>
                  {memberCandidates.map(r => (
                    <label key={r.id} className={styles.memberItem}>
                      <input
                        type="checkbox"
                        checked={form.memberNames.includes(r.name)}
                        onChange={() => toggleMember(r.name)}
                      />
                      <span className={styles.memberName}>{r.name}</span>
                      <span className={styles.memberType}>{r.type}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          )}

          {form.type !== 'group' && applicableCreate.length > 0 && (
            <div className={styles.formRow}>
              <label className={styles.label}>Cleanup policies</label>
              <div className={styles.memberList}>
                {applicableCreate.map(p => (
                  <label key={p.id} className={styles.memberItem}>
                    <input
                      type="checkbox"
                      checked={form.cleanupPolicyIds.includes(p.id)}
                      onChange={() =>
                        setForm(f => ({
                          ...f,
                          cleanupPolicyIds: f.cleanupPolicyIds.includes(p.id)
                            ? f.cleanupPolicyIds.filter(x => x !== p.id)
                            : [...f.cleanupPolicyIds, p.id],
                        }))
                      }
                    />
                    <span className={styles.memberName}>{p.name}</span>
                    <span className={styles.memberType}>{p.format === '*' ? 'all' : p.format}</span>
                  </label>
                ))}
              </div>
            </div>
          )}

          {form.type !== 'group' && (
            <div className={styles.formRow}>
              <label className={styles.label}>Blob Store *</label>
              {blobStores.length === 0 ? (
                <span className={styles.hint}>No blob stores configured. Create one in System Admin → Blob Stores.</span>
              ) : (
                <Select
                  options={blobStores.map(b => ({ value: b.id, label: `${b.name} (${b.type})` }))}
                  value={form.blobStoreId || defaultStoreId}
                  onChange={v => setField('blobStoreId', v)}
                />
              )}
              {(() => {
                const sel = blobStores.find(b => b.id === (form.blobStoreId || defaultStoreId))
                if (!sel) return <span className={styles.hint}>Physical storage backend where artifacts are written.</span>
                if (sel.quotaBytes == null) {
                  return <span className={styles.hint}>Store quota: unlimited.</span>
                }
                const free = sel.quotaBytes - (sel.usedBytes ?? 0)
                return (
                  <span className={styles.hint}>
                    Store quota: {formatBytes(sel.quotaBytes)} · free {formatBytes(free)}
                  </span>
                )
              })()}
            </div>
          )}

          <div className={styles.formRow}>
            <label className={styles.label}>Description</label>
            <input
              className={styles.input}
              value={form.description}
              onChange={e => setField('description', e.target.value)}
              placeholder="Optional description"
            />
          </div>

          {form.type !== 'group' && (
            <div className={styles.formRow}>
              <label className={styles.label}>Storage quota (GB)</label>
              <input
                className={styles.input}
                type="number"
                min="0"
                step="0.1"
                value={form.quotaGB}
                onChange={e => setField('quotaGB', e.target.value)}
                placeholder="No limit"
              />
              <span className={styles.hint}>Leave blank for unlimited storage</span>
            </div>
          )}

          <div className={styles.formRow}>
            <label className={styles.label}>Anonymous access</label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'rgba(229,231,235,0.75)', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={form.allowAnonymous}
                onChange={e => setField('allowAnonymous', e.target.checked)}
              />
              Allow unauthenticated read access
            </label>
            <span className={styles.hint}>When disabled, only users with an assigned role can read this repository.</span>
          </div>

          {error && <div className={styles.error}>{error}</div>}
          <div className={styles.modalFooter}>
            <button type="button" className={styles.cancelBtn} onClick={onClose}>Cancel</button>
            <button type="submit" className={styles.submitBtn} disabled={loading}>
              {loading ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

function EditRepoModal({
  repo,
  onClose,
  onSaved,
}: {
  repo: Repository
  onClose: () => void
  onSaved: () => void
}) {
  const { data: policies = [] } = useQuery<CleanupPolicyRow[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })

  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })

  const applicable = cleanupPoliciesForFormat(policies, repo.format)
  const [description, setDescription] = useState(repo.description ?? '')
  const [online, setOnline] = useState(repo.online)
  const [allowAnonymous, setAllowAnonymous] = useState(repo.allowAnonymous ?? false)
  const [policyIds, setPolicyIds] = useState<string[]>(repo.cleanupPolicyIds ?? [])
  const [quotaGB, setQuotaGB] = useState(
    repo.quotaBytes != null ? String(repo.quotaBytes / (1024 * 1024 * 1024)) : ''
  )
  const [blobStoreId, setBlobStoreId] = useState<string>(repo.blobStoreId ?? '')
  const originalStoreId = repo.blobStoreId ?? ''
  const storeChanged = blobStoreId !== originalStoreId
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const togglePolicy = (id: string) => {
    setPolicyIds(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id],
    )
  }

  const effectiveStoreId = blobStoreId || originalStoreId
  const selectedStore = blobStores.find(b => b.id === effectiveStoreId)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    // Enforce repo quota <= blob store quota (backend also checks).
    if (repo.type !== 'group' && quotaGB.trim() !== '') {
      const gb = parseFloat(quotaGB)
      if (!isNaN(gb) && gb > 0 && selectedStore?.quotaBytes != null) {
        const repoBytes = Math.round(gb * 1024 * 1024 * 1024)
        if (repoBytes > selectedStore.quotaBytes) {
          setError(`Repository quota (${formatBytes(repoBytes)}) exceeds blob store "${selectedStore.name}" quota (${formatBytes(selectedStore.quotaBytes)})`)
          return
        }
      }
    }

    setLoading(true)
    try {
      const updateBody: Record<string, unknown> = { description, online, allowAnonymous, cleanupPolicyIds: policyIds }
      if (quotaGB.trim() !== '') {
        const gb = parseFloat(quotaGB)
        if (!isNaN(gb) && gb > 0) {
          updateBody.quotaBytes = Math.round(gb * 1024 * 1024 * 1024)
        }
      } else {
        updateBody.quotaBytes = null
      }
      if (repo.type !== 'group' && blobStoreId) {
        updateBody.blobStoreId = blobStoreId
      }
      await nexusApi.updateRepository(repo.format, repo.type, repo.name, updateBody)
      onSaved()
    } catch (err: unknown) {
      const ax = err as { response?: { data?: { error?: string } } }
      setError(ax.response?.data?.error ?? 'Failed to save')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={styles.modalOverlay} onClick={onClose}>
      <div className={styles.modal} onClick={e => e.stopPropagation()}>
        <h2 className={styles.modalTitle}>Repository settings</h2>
        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.formRow}>
            <label className={styles.label}>Name</label>
            <input className={`${styles.input} ${styles.inputDisabled}`} value={repo.name} readOnly />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Online</label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'rgba(229,231,235,0.75)', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={online}
                onChange={e => setOnline(e.target.checked)}
              />
              Accept incoming requests
            </label>
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Anonymous access</label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'rgba(229,231,235,0.75)', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={allowAnonymous}
                onChange={e => setAllowAnonymous(e.target.checked)}
              />
              Allow unauthenticated read access
            </label>
            <span className={styles.hint}>When disabled, only users with an assigned role can read this repository.</span>
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Description</label>
            <input
              className={styles.input}
              value={description}
              onChange={e => setDescription(e.target.value)}
              placeholder="Optional"
            />
          </div>
          {repo.type !== 'group' && blobStores.length > 0 && (
            <div className={styles.formRow}>
              <label className={styles.label}>Blob Store</label>
              <Select
                options={blobStores.map(b => ({ value: b.id, label: `${b.name} (${b.type})` }))}
                value={blobStoreId || originalStoreId}
                onChange={setBlobStoreId}
              />
              {storeChanged ? (
                <span className={styles.hint} style={{ color: '#f59e0b' }}>
                  ⚠ Existing artifacts stay on the original store. Only future uploads land on the new one.
                </span>
              ) : (
                <span className={styles.hint}>Physical storage backend for new uploads.</span>
              )}
              {selectedStore && selectedStore.quotaBytes != null && (
                <span className={styles.hint}>
                  Store quota: {formatBytes(selectedStore.quotaBytes)} · free {formatBytes(selectedStore.quotaBytes - (selectedStore.usedBytes ?? 0))}
                </span>
              )}
            </div>
          )}

          {repo.type !== 'group' && (
            <div className={styles.formRow}>
              <label className={styles.label}>Storage quota (GB)</label>
              <input
                className={styles.input}
                type="number"
                min="0"
                step="0.1"
                value={quotaGB}
                onChange={e => setQuotaGB(e.target.value)}
                placeholder="No limit"
              />
              <span className={styles.hint}>Leave blank to remove the quota limit</span>
            </div>
          )}
          <div className={styles.formRow}>
            <label className={styles.label}>Cleanup policies</label>
            {applicable.length === 0 ? (
              <p className={styles.hint}>Create policies on the Cleanup page first.</p>
            ) : (
              <div className={styles.memberList}>
                {applicable.map(p => (
                  <label key={p.id} className={styles.memberItem}>
                    <input
                      type="checkbox"
                      checked={policyIds.includes(p.id)}
                      onChange={() => togglePolicy(p.id)}
                    />
                    <span className={styles.memberName}>{p.name}</span>
                    <span className={styles.memberType}>{p.format === '*' ? 'all' : p.format}</span>
                  </label>
                ))}
              </div>
            )}
            <span className={styles.hint}>Scheduled and manual runs only affect attached repositories.</span>
          </div>
          {error && <div className={styles.error}>{error}</div>}
          <div className={styles.modalFooter}>
            <button type="button" className={styles.cancelBtn} onClick={onClose}>Cancel</button>
            <button type="submit" className={styles.submitBtn} disabled={loading}>
              {loading ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
