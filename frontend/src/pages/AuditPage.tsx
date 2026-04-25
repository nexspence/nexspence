import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FileText, RefreshCw, ChevronLeft, ChevronRight, Download } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'
import { HoloButton, HoloInput, HoloCard, HoloPill, HoloText } from '@/components/holo'

interface AuditEvent {
  id: number
  eventTime: string
  username: string
  remoteIp: string
  domain: string
  action: string
  entityType: string
  entityName: string
  result: string
  context?: Record<string, any>
}

const DOMAINS = ['', 'REPOSITORY', 'SECURITY', 'USER', 'BLOBSTORE', 'CLEANUP', 'SYSTEM']
const ACTIONS = ['', 'CREATE', 'UPDATE', 'DELETE', 'LOGIN', 'LOGOUT']
const PAGE_SIZE = 50

const DOMAIN_COLOR: Record<string, string> = {
  REPOSITORY: '#3b82f6',
  SECURITY:   '#a78bfa',
  USER:       '#06b6d4',
  BLOBSTORE:  '#f59e0b',
  CLEANUP:    '#ef4444',
  SYSTEM:     '#6b7280',
}

const ACTION_COLOR: Record<string, string> = {
  CREATE: '#22c55e',
  UPDATE: '#3b82f6',
  DELETE: '#ef4444',
  LOGIN:  '#f59e0b',
  LOGOUT: '#6b7280',
}

const RESULT_COLOR: Record<string, string> = {
  success: '#22c55e',
  failure: '#ef4444',
  denied:  '#f59e0b',
}

function fmt(ts: string) {
  return new Date(ts).toLocaleString(undefined, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

interface AuditResponse { items: AuditEvent[]; total: number }

export default function AuditPage() {
  const [domain, setDomain] = useState('')
  const [action, setAction] = useState('')
  const [username, setUsername] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [offset, setOffset] = useState(0)

  const { data, isLoading, refetch } = useQuery<AuditResponse>({
    queryKey: ['audit', domain, action, username, from, to, offset],
    queryFn: () =>
      nexusApi
        .listAuditEvents({
          domain:   domain   || undefined,
          action:   action   || undefined,
          username: username || undefined,
          from:     from     || undefined,
          to:       to       || undefined,
          limit:    PAGE_SIZE,
          offset,
        })
        .then(r => {
          const body = r.data as Partial<AuditResponse>
          return { items: body.items ?? [], total: body.total ?? 0 }
        }),
  })

  const events = data?.items ?? []
  const total  = data?.total ?? 0
  const hasPrev = offset > 0
  const hasNext = offset + events.length < total

  const onExport = async () => {
    const url = nexusApi.auditExportUrl({
      domain:   domain   || undefined,
      action:   action   || undefined,
      username: username || undefined,
      from:     from     || undefined,
      to:       to       || undefined,
    })
    const token = localStorage.getItem('nexspence_token')
    const res = await fetch(url, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!res.ok) {
      alert(`Export failed: ${res.status} ${res.statusText}`)
      return
    }
    const blob = await res.blob()
    const objectUrl = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = objectUrl
    a.download = `audit-${new Date().toISOString().slice(0, 10)}.ndjson`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(objectUrl)
  }

  const resetOffset = () => setOffset(0)

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 6 }}>ADMINISTRATION / AUDIT</div>
          <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
            <HoloText>Audit Log</HoloText>
          </h1>
          <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>All system mutations — repository, user, and security events</p>
        </div>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          <Select
            options={DOMAINS.map(d => ({ value: d, label: d || 'All domains' }))}
            value={domain}
            onChange={v => { setDomain(v); resetOffset() }}
            style={{ minWidth: 160 }}
          />
          <Select
            options={ACTIONS.map(a => ({ value: a, label: a || 'All actions' }))}
            value={action}
            onChange={v => { setAction(v); resetOffset() }}
            style={{ minWidth: 140 }}
          />
          <HoloInput
            type="text"
            placeholder="username…"
            value={username}
            onChange={e => { setUsername(e.target.value); resetOffset() }}
            style={{ minWidth: 140 }}
          />
          <HoloInput
            type="date"
            value={from}
            onChange={e => { setFrom(e.target.value); resetOffset() }}
            title="From"
          />
          <HoloInput
            type="date"
            value={to}
            onChange={e => { setTo(e.target.value); resetOffset() }}
            title="To"
          />
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={15} /></HoloButton>
          <HoloButton onClick={onExport} title="Export filtered events as NDJSON"><Download size={15} /></HoloButton>
        </div>
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>Loading…</div>
      ) : events.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>
          <FileText size={40} style={{ opacity: 0.3 }} />
          <p>No audit events{domain || action || username || from || to ? ' matching filters' : ''}</p>
        </div>
      ) : (
        <>
          <HoloCard style={{ padding: 0 }}>
            <table className="holo-table">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>User</th>
                  <th>Domain</th>
                  <th>Action</th>
                  <th>Entity</th>
                  <th>Path</th>
                  <th>IP</th>
                  <th>Result</th>
                </tr>
              </thead>
              <tbody>
                {events.map(e => (
                  <tr key={e.id}>
                    <td style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--holo-text-dim)' }}>{fmt(e.eventTime)}</td>
                    <td style={{ fontWeight: 500 }}>{e.username || '—'}</td>
                    <td>
                      <HoloPill style={{ background: (DOMAIN_COLOR[e.domain] ?? '#6b7280') + '20', color: DOMAIN_COLOR[e.domain] ?? '#6b7280' }}>
                        {e.domain}
                      </HoloPill>
                    </td>
                    <td>
                      <HoloPill style={{ background: (ACTION_COLOR[e.action] ?? '#6b7280') + '20', color: ACTION_COLOR[e.action] ?? '#6b7280' }}>
                        {e.action}
                      </HoloPill>
                    </td>
                    <td style={{ color: 'rgba(229,231,235,0.7)' }}>
                      {e.entityType ? `${e.entityType}: ` : ''}
                      <span style={{ color: 'var(--holo-a)' }}>{e.entityName || '—'}</span>
                    </td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--holo-text-dim)', maxWidth: 320 }}>
                      {e.context?.path
                        ? <span title={String(e.context.path)} style={{ display: 'inline-block', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{String(e.context.path)}</span>
                        : '—'}
                    </td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--holo-text-dim)' }}>{e.remoteIp || '—'}</td>
                    <td>
                      <HoloPill style={{ background: (RESULT_COLOR[e.result] ?? '#6b7280') + '20', color: RESULT_COLOR[e.result] ?? '#6b7280' }}>
                        {e.result}
                      </HoloPill>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </HoloCard>

          <div style={{ display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'flex-end', fontSize: 13, color: 'var(--holo-text-dim)' }}>
            <span>Showing {offset + 1}–{offset + events.length} of {total}</span>
            <HoloButton disabled={!hasPrev} style={{ opacity: hasPrev ? 1 : 0.4 }}
              onClick={() => setOffset(o => Math.max(0, o - PAGE_SIZE))}>
              <ChevronLeft size={15} />
            </HoloButton>
            <HoloButton disabled={!hasNext} style={{ opacity: hasNext ? 1 : 0.4 }}
              onClick={() => setOffset(o => o + PAGE_SIZE)}>
              <ChevronRight size={15} />
            </HoloButton>
          </div>
        </>
      )}
    </div>
  )
}
