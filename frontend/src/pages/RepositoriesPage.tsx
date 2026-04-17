import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Database, Plus, Trash2, RefreshCw, ChevronDown } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'
import styles from './RepositoriesPage.module.css'

interface Repository {
  id: string
  name: string
  format: string
  type: string
  online: boolean
  description?: string
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
  const [filter, setFilter] = useState('')
  const [formatFilter, setFormatFilter] = useState('')
  const [showCreate, setShowCreate] = useState(false)

  const { data: repos = [], isLoading, refetch } = useQuery<Repository[]>({
    queryKey: ['repositories', formatFilter],
    queryFn: () =>
      nexusApi.listRepositories(formatFilter ? { format: formatFilter } : {})
        .then(r => r.data),
  })

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
          <button className={styles.createBtn} onClick={() => setShowCreate(true)}>
            <Plus size={16} />
            Create Repository
          </button>
        </div>
      </div>

      <div className={styles.toolbar}>
        <input
          className={styles.search}
          placeholder="Filter by name…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
        <div className={styles.selectWrapper}>
          <select
            className={styles.select}
            value={formatFilter}
            onChange={e => setFormatFilter(e.target.value)}
          >
            <option value="">All formats</option>
            {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => (
              <option key={f} value={f}>{f}</option>
            ))}
          </select>
          <ChevronDown size={14} className={styles.selectIcon} />
        </div>
      </div>

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : filtered.length === 0 ? (
        <div className={styles.empty}>
          <Database size={40} className={styles.emptyIcon} />
          <p>No repositories found</p>
          {!filter && (
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
    </div>
  )
}

function RepoCard({ repo, onDelete }: { repo: Repository; onDelete: () => void }) {
  const color = FORMAT_COLORS[repo.format] ?? '#6b7280'
  return (
    <div className={styles.card}>
      <div className={styles.cardHeader}>
        <span className={styles.formatBadge} style={{ background: color + '22', color }}>
          {repo.format}
        </span>
        <span className={`${styles.typeBadge} ${styles['type_' + repo.type]}`}>
          {TYPE_LABELS[repo.type] ?? repo.type}
        </span>
        <span className={`${styles.statusDot} ${repo.online ? styles.online : styles.offline}`} />
      </div>
      <div className={styles.cardName}>{repo.name}</div>
      {repo.description && (
        <div className={styles.cardDesc}>{repo.description}</div>
      )}
      <div className={styles.cardFooter}>
        <button className={styles.deleteBtn} onClick={onDelete} title="Delete">
          <Trash2 size={14} />
        </button>
      </div>
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

  const [form, setForm] = useState({
    name: '', format: 'maven2', type: 'hosted', description: '',
    remoteUrl: PROXY_DEFAULTS['maven2'],
    memberNames: [] as string[],
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

    setLoading(true)
    try {
      const body: Record<string, unknown> = {
        name: form.name,
        description: form.description,
      }
      if (form.type === 'proxy') {
        body.proxyConfig = { remote_url: form.remoteUrl.trim() }
      }
      if (form.type === 'group') {
        body.formatConfig = { member_names: form.memberNames }
      }

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
            <select
              className={styles.input}
              value={form.format}
              onChange={e => handleFormatChange(e.target.value)}
            >
              {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum','cargo','conan'].map(f => (
                <option key={f} value={f}>{f}</option>
              ))}
            </select>
          </div>

          <div className={styles.formRow}>
            <label className={styles.label}>Type</label>
            <select
              className={styles.input}
              value={form.type}
              onChange={e => {
                const t = e.target.value
                setForm(f => ({
                  ...f,
                  type: t,
                  remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
                }))
              }}
            >
              <option value="hosted">Hosted — store artifacts locally</option>
              <option value="proxy">Proxy — cache from remote registry</option>
              <option value="group">Group — combine multiple repos</option>
            </select>
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

          <div className={styles.formRow}>
            <label className={styles.label}>Description</label>
            <input
              className={styles.input}
              value={form.description}
              onChange={e => setField('description', e.target.value)}
              placeholder="Optional description"
            />
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
