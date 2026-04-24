import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FileText, RefreshCw, ChevronLeft, ChevronRight, Download } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'

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

const S = {
  page:    { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:  { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:   { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:{ fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  filters: { display: 'flex', gap: 10, flexWrap: 'wrap' as const, alignItems: 'center' },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
  input:   { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 10px', color: '#e5e7eb', fontSize: 13, fontFamily: 'inherit' },
  table:   { width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 },
  th:      { textAlign: 'left' as const, padding: '8px 12px', color: 'rgba(229,231,235,0.45)', fontWeight: 600, fontSize: 11, textTransform: 'uppercase' as const, letterSpacing: '0.05em', borderBottom: '1px solid rgba(255,255,255,0.06)' },
  td:      { padding: '10px 12px', borderBottom: '1px solid rgba(255,255,255,0.04)', verticalAlign: 'middle' as const },
  mono:    { fontFamily: 'monospace', fontSize: 12, color: 'rgba(229,231,235,0.6)' },
  badge:   (color: string) => ({
    fontSize: 11, fontWeight: 600 as const, padding: '2px 7px',
    borderRadius: 4, background: color + '20', color,
    display: 'inline-block',
  }),
  empty:   { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.35)', fontSize: 14, paddingTop: 48 },
  pagination:{ display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'flex-end', fontSize: 13, color: 'rgba(229,231,235,0.5)' },
  card:    { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  pathCell:{ display: 'inline-block', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
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

  const onExport = () => {
    const url = nexusApi.auditExportUrl({
      domain:   domain   || undefined,
      action:   action   || undefined,
      username: username || undefined,
      from:     from     || undefined,
      to:       to       || undefined,
    })
    window.location.href = url
  }

  const resetOffset = () => setOffset(0)

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Audit Log</h1>
          <p style={S.subtitle}>All system mutations — repository, user, and security events</p>
        </div>
        <div style={S.filters}>
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
          <input
            type="text"
            placeholder="username…"
            value={username}
            onChange={e => { setUsername(e.target.value); resetOffset() }}
            style={{ ...S.input, minWidth: 140 }}
          />
          <input
            type="date"
            value={from}
            onChange={e => { setFrom(e.target.value); resetOffset() }}
            style={S.input}
            title="From"
          />
          <input
            type="date"
            value={to}
            onChange={e => { setTo(e.target.value); resetOffset() }}
            style={S.input}
            title="To"
          />
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={15} />
          </button>
          <button style={S.iconBtn} onClick={onExport} title="Export filtered events as NDJSON">
            <Download size={15} />
          </button>
        </div>
      </div>

      {isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : events.length === 0 ? (
        <div style={S.empty}>
          <FileText size={40} style={{ opacity: 0.3 }} />
          <p>No audit events{domain || action || username || from || to ? ' matching filters' : ''}</p>
        </div>
      ) : (
        <>
          <div style={S.card}>
            <table style={S.table}>
              <thead>
                <tr>
                  <th style={S.th}>Time</th>
                  <th style={S.th}>User</th>
                  <th style={S.th}>Domain</th>
                  <th style={S.th}>Action</th>
                  <th style={S.th}>Entity</th>
                  <th style={S.th}>Path</th>
                  <th style={S.th}>IP</th>
                  <th style={S.th}>Result</th>
                </tr>
              </thead>
              <tbody>
                {events.map(e => (
                  <tr key={e.id} style={{ color: '#e5e7eb' }}>
                    <td style={{ ...S.td, ...S.mono }}>{fmt(e.eventTime)}</td>
                    <td style={{ ...S.td, fontWeight: 500 }}>{e.username || '—'}</td>
                    <td style={S.td}>
                      <span style={S.badge(DOMAIN_COLOR[e.domain] ?? '#6b7280')}>
                        {e.domain}
                      </span>
                    </td>
                    <td style={S.td}>
                      <span style={S.badge(ACTION_COLOR[e.action] ?? '#6b7280')}>
                        {e.action}
                      </span>
                    </td>
                    <td style={{ ...S.td, color: 'rgba(229,231,235,0.7)' }}>
                      {e.entityType ? `${e.entityType}: ` : ''}
                      <span style={{ color: '#93c5fd' }}>{e.entityName || '—'}</span>
                    </td>
                    <td style={{ ...S.td, ...S.mono, maxWidth: 320 }}>
                      {e.context?.path
                        ? <span title={String(e.context.path)} style={S.pathCell}>{String(e.context.path)}</span>
                        : '—'}
                    </td>
                    <td style={{ ...S.td, ...S.mono }}>{e.remoteIp || '—'}</td>
                    <td style={S.td}>
                      <span style={S.badge(RESULT_COLOR[e.result] ?? '#6b7280')}>
                        {e.result}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div style={S.pagination}>
            <span>Showing {offset + 1}–{offset + events.length} of {total}</span>
            <button style={{ ...S.iconBtn, opacity: hasPrev ? 1 : 0.4 }}
              disabled={!hasPrev} onClick={() => setOffset(o => Math.max(0, o - PAGE_SIZE))}>
              <ChevronLeft size={15} />
            </button>
            <button style={{ ...S.iconBtn, opacity: hasNext ? 1 : 0.4 }}
              disabled={!hasNext} onClick={() => setOffset(o => o + PAGE_SIZE)}>
              <ChevronRight size={15} />
            </button>
          </div>
        </>
      )}
    </div>
  )
}
