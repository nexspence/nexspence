import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { HoloButton, HoloInput, HoloText, HoloModal } from '@/components/holo'
import {
  ChevronDown,
  ChevronRight,
  Download,
  FileText,
  FolderOpen,
  Layers,
  Link,
  Package,
  RefreshCw,
  ShieldAlert,
  Tag,
  Trash2,
  Upload,
} from 'lucide-react'
import axios from 'axios'
import { nexusApi, nexspenceApi, apiClient, Privilege } from '@/api/client'
import { Select, SelectOption } from '../components/Select'
import { useAuthStore } from '@/store/authStore'

interface Repository {
  id: string
  name: string
  format: string
  type: string
}
interface Component {
  id: string
  name: string
  group: string
  version: string
  format: string
  assets?: { id: string; path: string; fileSize: number; contentType: string }[]
}

interface DockerDetailAsset {
  path: string
  fileSize: number
  contentType: string
  createdAt: string
  lastModified: string
  lastDownloaded?: string | null
  downloadCount?: number
  blobStoreId?: string
  blobKey?: string
  downloadUrl?: string
  /** Joined uploader username (Nexus "Uploader") */
  uploader?: string
}

interface DockerComponentDetail {
  id: string
  repository: string
  format: string
  name: string
  version: string
  group: string
  createdAt?: string
  downloadCount?: number
  lastDownloaded?: string | null
  assets?: DockerDetailAsset[]
}

interface DockerTreeNode {
  kind: 'folder' | 'tag' | 'manifest' | 'blob'
  label: string
  path: string
  imageRef?: string
  version?: string
  componentId?: string
  children?: DockerTreeNode[]
}

interface DockerLeafSelection {
  path: string
  kind: DockerTreeNode['kind']
  componentId: string
  imageRef?: string
  version?: string
}

interface RawTreeNode {
  kind: 'folder' | 'file'
  label: string
  path: string
  size?: number
  sha256?: string
  contentType?: string
  updatedAt?: string
  componentId?: string
  children?: RawTreeNode[]
}

interface RawFileSelection {
  path: string
  node: RawTreeNode
}

interface ScanSummary {
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
  total: number
}

interface CVEFinding {
  id: string
  severity: string
  pkgName: string
  installedVersion: string
  fixedVersion?: string
  title?: string
}

interface ScanResult {
  scannedAt: string
  imageRef: string
  status: 'ok' | 'failed'
  error?: string
  summary: ScanSummary
  findings?: CVEFinding[]
}

const SEV_COLOR = {
  critical: '#ef4444',
  high: '#f97316',
  medium: '#f59e0b',
  low: '#22c55e',
  unknown: '#6b7280',
} as const

function sevChipColor(sev: string) {
  const k = sev.toUpperCase()
  if (k === 'CRITICAL') return SEV_COLOR.critical
  if (k === 'HIGH') return SEV_COLOR.high
  if (k === 'MEDIUM') return SEV_COLOR.medium
  if (k === 'LOW') return SEV_COLOR.low
  return SEV_COLOR.unknown
}

function CveBadge({ label, count, color }: { label: string; count: number; color: string }) {
  if (count === 0) return null
  return (
    <span
      style={{
        fontSize: 11,
        fontWeight: 700,
        padding: '2px 7px',
        borderRadius: 4,
        background: color + '22',
        color,
        border: '1px solid ' + color + '55',
        marginRight: 4,
      }}
    >
      {label}: {count}
    </span>
  )
}

const SCAN_SEVERITY_FILTERS = ['ALL', 'CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN'] as const

function fmtElapsed(s: number): string {
  const m = Math.floor(s / 60)
  return m > 0 ? `${m}m ${s % 60}s` : `${s}s`
}

function ScanBadgeRow({ componentId }: { componentId: string }) {
  const queryClient = useQueryClient()
  const queryKey = ['scanResult', componentId]
  const [mutationError, setMutationError] = useState<string | null>(null)
  const [sevFilter, setSevFilter] = useState<(typeof SCAN_SEVERITY_FILTERS)[number]>('ALL')
  const [elapsed, setElapsed] = useState(0)

  const { data: scanResult, isLoading } = useQuery<ScanResult | null>({
    queryKey,
    queryFn: () =>
      nexspenceApi
        .getScanResult(componentId)
        .then((r) => (r.data as ScanResult | null) ?? null)
        .catch((e) => (e.response?.status === 404 ? null : Promise.reject(e))),
    retry: false,
  })

  const scanMutation = useMutation({
    mutationFn: () => nexspenceApi.scanComponent(componentId),
    onSuccess: (response) => {
      setMutationError(null)
      setSevFilter('ALL')
      setElapsed(0)
      queryClient.setQueryData(queryKey, response.data as ScanResult)
    },
    onError: (e: unknown) => {
      const msg =
        (e as { response?: { data?: { error?: string } } })?.response?.data?.error ??
        (e instanceof Error ? e.message : 'Unknown error')
      setMutationError(msg)
    },
  })

  useEffect(() => {
    if (!scanMutation.isPending) { setElapsed(0); return }
    const t = setInterval(() => setElapsed((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [scanMutation.isPending])

  const s = scanResult?.summary
  const findings = scanResult?.findings ?? []
  const filtered =
    sevFilter === 'ALL' ? findings : findings.filter((f) => f.severity?.toUpperCase() === sevFilter)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: '10px 0 0' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <ShieldAlert size={14} style={{ color: '#60a5fa', flexShrink: 0 }} />
        <span style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>Vulnerability scan</span>
        {!scanMutation.isPending && scanResult && (
          <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
            {new Date(scanResult.scannedAt).toLocaleString()}
          </span>
        )}
        <HoloButton
          variant="primary"
          onClick={() => {
            setMutationError(null)
            scanMutation.mutate()
          }}
          disabled={scanMutation.isPending}
          style={{ marginLeft: 'auto', fontSize: 11, padding: '3px 10px' }}
        >
          {scanMutation.isPending ? `Scanning… ${fmtElapsed(elapsed)}` : 'Scan now'}
        </HoloButton>
      </div>
      {mutationError && (
        <span style={{ fontSize: 11, color: '#ef4444' }}>Error: {mutationError}</span>
      )}
      {scanMutation.isPending && (
        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)', lineHeight: 1.4 }}>
          Running Trivy vulnerability scan
          {elapsed >= 20 && ' — first run downloads the vulnerability DB (~2 min)'}
          {elapsed >= 90 && '; please wait…'}
        </span>
      )}
      {!scanMutation.isPending && isLoading ? (
        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>Loading…</span>
      ) : !scanMutation.isPending && scanResult?.status === 'failed' ? (
        <span style={{ fontSize: 11, color: '#ef4444' }}>Scan failed: {scanResult.error}</span>
      ) : !scanMutation.isPending && s ? (
        <>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 2 }}>
            <CveBadge label="CRITICAL" count={s.critical} color={SEV_COLOR.critical} />
            <CveBadge label="HIGH" count={s.high} color={SEV_COLOR.high} />
            <CveBadge label="MEDIUM" count={s.medium} color={SEV_COLOR.medium} />
            <CveBadge label="LOW" count={s.low} color={SEV_COLOR.low} />
            {s.unknown > 0 && (
              <CveBadge label="UNKNOWN" count={s.unknown} color={SEV_COLOR.unknown} />
            )}
            {s.total === 0 && (
              <span style={{ fontSize: 11, color: '#22c55e', fontWeight: 600 }}>No vulnerabilities found</span>
            )}
          </div>
          {scanResult?.status === 'ok' && findings.length > 0 && (
            <div style={{ marginTop: 4 }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 8 }}>
                {SCAN_SEVERITY_FILTERS.map((f) => (
                  <button
                    key={f}
                    type="button"
                    onClick={() => setSevFilter(f)}
                    style={{
                      fontSize: 10,
                      fontWeight: 600,
                      padding: '3px 8px',
                      borderRadius: 4,
                      border: 'none',
                      cursor: 'pointer',
                      background:
                        sevFilter === f
                          ? f === 'ALL'
                            ? '#3b82f6'
                            : sevChipColor(f)
                          : 'rgba(255,255,255,0.06)',
                      color: sevFilter === f ? '#fff' : 'var(--holo-text-dim)',
                    }}
                  >
                    {f}
                    {f !== 'ALL' && ` (${findings.filter((x) => x.severity?.toUpperCase() === f).length})`}
                  </button>
                ))}
              </div>
              <div
                style={{
                  maxHeight: 280,
                  overflowY: 'auto' as const,
                  border: '1px solid rgba(255,255,255,0.08)',
                  borderRadius: 8,
                  fontSize: 11,
                }}
              >
                <table style={{ width: '100%', borderCollapse: 'collapse' as const }}>
                  <thead>
                    <tr style={{ color: 'var(--holo-text-faint)', textAlign: 'left' as const }}>
                      <th style={{ padding: '8px 10px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>CVE</th>
                      <th style={{ padding: '8px 6px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>Sev</th>
                      <th style={{ padding: '8px 6px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>Package</th>
                      <th style={{ padding: '8px 6px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>Installed</th>
                      <th style={{ padding: '8px 6px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>Fixed</th>
                      <th style={{ padding: '8px 6px', fontWeight: 600, position: 'sticky', top: 0, background: '#0c1018' }}>Title</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((row, i) => (
                      <tr key={`${row.id}-${row.pkgName}-${i}`} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                        <td style={{ padding: '6px 10px', fontFamily: 'monospace', color: '#a5b4fc' }}>{row.id}</td>
                        <td style={{ padding: '6px 6px' }}>
                          <span
                            style={{
                              fontSize: 10,
                              fontWeight: 700,
                              padding: '1px 5px',
                              borderRadius: 3,
                              background: sevChipColor(row.severity) + '33',
                              color: sevChipColor(row.severity),
                            }}
                          >
                            {row.severity}
                          </span>
                        </td>
                        <td style={{ padding: '6px 6px', color: 'var(--holo-text)' }}>{row.pkgName}</td>
                        <td style={{ padding: '6px 6px', fontFamily: 'monospace', color: 'var(--holo-text-dim)' }}>
                          {row.installedVersion}
                        </td>
                        <td style={{ padding: '6px 6px', fontFamily: 'monospace', color: '#86efac' }}>{row.fixedVersion || '—'}</td>
                        <td
                          style={{
                            padding: '6px 6px',
                            color: 'var(--holo-text-dim)',
                            maxWidth: 200,
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap' as const,
                          }}
                          title={row.title || undefined}
                        >
                          {row.title || '—'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {filtered.length === 0 && (
                  <div style={{ padding: 12, color: 'var(--holo-text-faint)' }}>No rows for this filter.</div>
                )}
              </div>
            </div>
          )}
        </>
      ) : !scanMutation.isPending ? (
        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>Not scanned yet</span>
      ) : null}
    </div>
  )
}

function formatBytes(n: number): string {
  if (n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

function formatDateTime(iso: string | undefined | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

function nexusV2RegistryPath(
  imageRef: string | undefined,
  version: string | undefined,
  kind: DockerTreeNode['kind'],
): string {
  const img = (imageRef ?? '').trim()
  const v = (version ?? '').trim()
  if (!img || !v) return '—'
  if (kind === 'blob') return `v2/${img}/blobs/${v}`
  return `v2/${img}/manifests/${v}`
}

function pickPrimaryDockerAsset(
  assets: DockerDetailAsset[] | undefined,
  kind: DockerTreeNode['kind'],
  version: string,
): DockerDetailAsset | undefined {
  if (!assets?.length) return undefined
  const v = version.trim()
  if (kind === 'blob') {
    return assets.find((a) => a.path.includes('/blobs/') && (a.path.endsWith('/' + v) || a.path.endsWith(v)))
  }
  if (kind === 'tag' || kind === 'manifest') {
    const m = assets.find((a) => a.path.includes('/manifests/') && (a.path.endsWith('/' + v) || a.path.endsWith(v)))
    if (m) return m
    return assets.find((a) => a.path.includes('/manifests/'))
  }
  return assets[0]
}

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20, height: '100%' },
  header: { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 },
  title: { fontSize: 20, fontWeight: 700, color: 'var(--holo-text)', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 },
  toolbar: { display: 'flex', gap: 12, alignItems: 'center' },
  iconBtn: {
    background: 'rgba(255,255,255,0.06)',
    border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8,
    padding: 8,
    color: 'var(--holo-text-dim)',
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
  },
  empty: {
    flex: 1,
    display: 'flex',
    flexDirection: 'column' as const,
    alignItems: 'center',
    justifyContent: 'center',
    gap: 12,
    color: 'var(--holo-text-faint)',
    fontSize: 14,
  },
  table: {
    overflow: 'hidden' as const,
  },
  thead: {
    display: 'grid',
    gridTemplateColumns: '2fr 1.5fr 1fr 1fr 2fr 32px',
    padding: '10px 16px',
    background: 'rgba(255,255,255,0.03)',
    borderBottom: '1px solid rgba(255,255,255,0.07)',
    fontSize: 11,
    fontWeight: 600,
    color: 'var(--holo-text-dim)',
    textTransform: 'uppercase' as const,
    letterSpacing: '0.05em',
  },
  trow: {
    display: 'grid',
    gridTemplateColumns: '2fr 1.5fr 1fr 1fr 2fr 32px',
    padding: '11px 16px',
    borderBottom: '1px solid rgba(255,255,255,0.05)',
    fontSize: 13,
    color: 'var(--holo-text)',
    alignItems: 'center',
  },
  badge: (color: string) => ({
    fontSize: 11,
    fontWeight: 600 as const,
    padding: '2px 8px',
    borderRadius: 4,
    background: color + '22',
    color,
  }),
  muted: { color: 'var(--holo-text-faint)', fontSize: 12 },
  path: { fontSize: 12, color: 'rgba(147,197,253,0.85)', fontFamily: 'monospace' as const },
  pager: { display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'center', paddingTop: 4 },
  pgBtn: (disabled: boolean) => ({
    background: disabled ? 'rgba(255,255,255,0.03)' : 'rgba(255,255,255,0.07)',
    border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8,
    padding: '6px 14px',
    color: disabled ? 'rgba(229,231,235,0.25)' : 'var(--holo-text)',
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
  }),
  treePanel: {
    padding: '12px 8px',
    maxHeight: 'calc(100vh - 220px)',
    overflowY: 'auto' as const,
  },
  treeRow: (depth: number) => ({
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    padding: '5px 8px 5px ' + (8 + depth * 16) + 'px',
    fontSize: 13,
    color: 'var(--holo-text)',
    borderRadius: 6,
    cursor: 'default' as const,
  }),
  treeFolder: { cursor: 'pointer' as const, userSelect: 'none' as const },
  treeHint: { fontSize: 11, color: 'var(--holo-text-faint)', padding: '0 12px 8px' },
  dockerLayout: {
    display: 'flex',
    gap: 16,
    alignItems: 'flex-start' as const,
    flexWrap: 'wrap' as const,
  },
  detailPanel: {
    flex: '1 1 320px',
    minWidth: 280,
    maxWidth: '100%',
    padding: '14px 16px',
    maxHeight: 'calc(100vh - 220px)',
    overflowY: 'auto' as const,
  },
  detailTitle: { fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', margin: '0 0 12px' },
  detailRow: {
    display: 'grid',
    gridTemplateColumns: '168px 1fr',
    gap: '8px 14px',
    padding: '7px 0',
    borderBottom: '1px solid rgba(255,255,255,0.06)',
    fontSize: 13,
  },
  detailLabel: { color: 'var(--holo-text-faint)', fontSize: 12 },
  detailValue: { color: 'var(--holo-text)', wordBreak: 'break-word' as const },
  detailActions: { display: 'flex', gap: 8, marginTop: 14 },
  btnDl: {
    padding: '7px 14px',
    background: 'rgba(59,130,246,0.15)',
    border: '1px solid rgba(59,130,246,0.4)',
    borderRadius: 8,
    color: '#93c5fd',
    fontSize: 12,
    cursor: 'pointer' as const,
    display: 'flex',
    alignItems: 'center',
    gap: 5,
  },
  btnCopy: {
    padding: '7px 14px',
    background: 'rgba(255,255,255,0.05)',
    border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8,
    color: 'var(--holo-text-dim)',
    fontSize: 12,
    cursor: 'pointer' as const,
    display: 'flex',
    alignItems: 'center',
    gap: 5,
  },
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316',
  npm: '#ef4444',
  docker: '#3b82f6',
  pypi: '#a78bfa',
  go: '#06b6d4',
  nuget: '#8b5cf6',
  helm: '#0ea5e9',
  raw: '#6b7280',
  apt: '#f59e0b',
  yum: '#10b981',
}

function DockerBrowseDetailBody({
  comp,
  sel,
}: {
  comp: DockerComponentDetail
  sel: DockerLeafSelection
}) {
  const primary = pickPrimaryDockerAsset(comp.assets, sel.kind, sel.version ?? '')
  const v2path = nexusV2RegistryPath(sel.imageRef, sel.version, sel.kind)
  const pathDisplay =
    primary?.path && primary.path !== v2path ? `${v2path} (${primary.path})` : v2path
  const blobRef =
    primary?.blobKey != null && primary.blobKey !== ''
      ? primary.blobStoreId
        ? `${primary.blobStoreId}@${primary.blobKey}`
        : primary.blobKey
      : '—'

  const rows: { label: string; value: string }[] = [
    { label: 'Repository', value: comp.repository },
    { label: 'Format', value: comp.format },
    { label: 'Component Name', value: comp.name },
    { label: 'Component Version', value: comp.version },
    { label: 'Path', value: pathDisplay },
    { label: 'Content type', value: primary?.contentType || '—' },
    { label: 'File size', value: primary != null ? formatBytes(primary.fileSize) : '—' },
    { label: 'Blob created', value: formatDateTime(primary?.createdAt) },
    { label: 'Blob updated', value: formatDateTime(primary?.lastModified) },
    {
      label: 'Last downloaded',
      value: formatDateTime(primary?.lastDownloaded ?? comp.lastDownloaded),
    },
    { label: 'Locally cached', value: primary ? 'true' : 'false' },
    { label: 'Blob reference', value: blobRef },
    { label: 'Containing repo', value: comp.repository },
    {
      label: 'Uploader',
      value: primary?.uploader?.trim() ? primary.uploader : 'anonymous',
    },
    { label: "Uploader's IP Address", value: '—' },
  ]

  return (
    <div>
      {rows.map((r) => (
        <div key={r.label} style={S.detailRow}>
          <div style={S.detailLabel}>{r.label}</div>
          <div style={S.detailValue}>{r.value}</div>
        </div>
      ))}
      <div style={{ borderTop: '1px solid rgba(255,255,255,0.08)', marginTop: 8 }}>
        <ScanBadgeRow componentId={sel.componentId} />
      </div>
    </div>
  )
}

function DockerTreeRows({
  node,
  depth,
  collapsed,
  toggle,
  selectedPath,
  onSelectLeaf,
  showDelete,
  onDelete,
}: {
  node: DockerTreeNode
  depth: number
  collapsed: Record<string, boolean>
  toggle: (p: string) => void
  selectedPath: string | null
  onSelectLeaf?: (node: DockerTreeNode) => void
  showDelete?: boolean
  onDelete?: (node: DockerTreeNode) => void
}) {
  const hasKids = !!(node.children && node.children.length > 0)
  const isFolder = node.kind === 'folder'
  const folded = collapsed[node.path] !== false

  if (!isFolder) {
    const icon =
      node.kind === 'manifest' ? (
        <FileText size={14} style={{ color: '#93c5fd', flexShrink: 0 }} />
      ) : node.kind === 'blob' ? (
        <Layers size={14} style={{ color: '#a78bfa', flexShrink: 0 }} />
      ) : (
        <Tag size={14} style={{ color: '#4ade80', flexShrink: 0 }} />
      )
    const clickable = !!(node.componentId && onSelectLeaf)
    const selected = selectedPath === node.path
    return (
      <div
        key={node.path}
        role={clickable ? 'button' : undefined}
        tabIndex={clickable ? 0 : undefined}
        onClick={(e) => {
          e.stopPropagation()
          if (clickable) onSelectLeaf!(node)
        }}
        onKeyDown={(e) => {
          if (clickable && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault()
            onSelectLeaf!(node)
          }
        }}
        style={{
          ...S.treeRow(depth),
          ...(clickable
            ? {
                cursor: 'pointer',
                background: selected ? 'rgba(59,130,246,0.12)' : undefined,
                outline: selected ? '1px solid rgba(59,130,246,0.35)' : undefined,
              }
            : {}),
        }}
      >
        {icon}
        <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{node.label}</span>
        {node.imageRef && <span style={S.muted}>— {node.imageRef}</span>}
        {showDelete && (node.kind === 'tag') && onDelete && (
          <button
            onClick={e => { e.stopPropagation(); onDelete(node) }}
            style={{ marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.6)', padding: '2px 4px', display: 'flex', alignItems: 'center', flexShrink: 0 }}
            title="Delete tag"
          >
            <Trash2 size={12} />
          </button>
        )}
      </div>
    )
  }

  return (
    <div key={node.path}>
      <div
        style={{ ...S.treeRow(depth), ...(hasKids ? S.treeFolder : {}) }}
        onClick={() => hasKids && toggle(node.path)}
        onKeyDown={(e) => {
          if (hasKids && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault()
            toggle(node.path)
          }
        }}
        role={hasKids ? 'button' : undefined}
        tabIndex={hasKids ? 0 : undefined}
      >
        {hasKids ? (
          folded ? (
            <ChevronRight size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
          ) : (
            <ChevronDown size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
          )
        ) : (
          <span style={{ width: 14 }} />
        )}
        <FolderOpen size={14} style={{ color: '#60a5fa', flexShrink: 0 }} />
        <span style={{ fontWeight: depth === 0 ? 600 : 500 }}>{node.label}</span>
        {showDelete && onDelete && !['Tags', 'Manifests', 'Blobs'].includes(node.label) && (
          <button
            onClick={e => { e.stopPropagation(); onDelete(node) }}
            style={{ marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.5)', padding: '2px 4px', display: 'flex', alignItems: 'center', flexShrink: 0 }}
            title={`Delete all in ${node.label}`}
          >
            <Trash2 size={12} />
          </button>
        )}
      </div>
      {hasKids && !folded && node.children!.map((ch) => (
        <DockerTreeRows
          key={ch.path}
          node={ch}
          depth={depth + 1}
          collapsed={collapsed}
          toggle={toggle}
          selectedPath={selectedPath}
          onSelectLeaf={onSelectLeaf}
          showDelete={showDelete}
          onDelete={onDelete}
        />
      ))}
    </div>
  )
}

function collectLeafPaths(node: RawTreeNode): string[] {
  if (node.kind === 'file') return [node.path]
  return (node.children ?? []).flatMap(collectLeafPaths)
}

function RawTreeRows({
  node,
  depth,
  collapsed,
  toggle,
  selectedPath,
  onSelectFile,
  showDelete,
  onDelete,
  repoName,
}: {
  node: RawTreeNode
  depth: number
  collapsed: Record<string, boolean>
  toggle: (p: string) => void
  selectedPath: string | null
  onSelectFile?: (node: RawTreeNode) => void
  showDelete?: boolean
  onDelete?: (node: RawTreeNode) => void
  repoName: string
}) {
  const [hovered, setHovered] = useState(false)

  if (node.kind === 'file') {
    const selected = selectedPath === node.path
    const cleanPath = node.path.replace(/^\//, '')
    const downloadUrl = `/repository/${repoName}/${cleanPath}`
    const copyUrl = `${window.location.origin}/repository/${repoName}/${cleanPath}`

    function doDownload() {
      void apiClient.get(downloadUrl, { responseType: 'blob' }).then((res) => {
        const url = window.URL.createObjectURL(res.data as Blob)
        const a = document.createElement('a')
        a.href = url
        a.download = node.label
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        window.URL.revokeObjectURL(url)
      })
    }

    function doCopy() {
      void navigator.clipboard.writeText(copyUrl)
    }

    return (
      <div
        role="button"
        tabIndex={0}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onClick={(e) => {
          e.stopPropagation()
          if (onSelectFile) onSelectFile(node)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            if (onSelectFile) onSelectFile(node)
          }
        }}
        style={{
          ...S.treeRow(depth),
          cursor: 'pointer',
          background: selected ? 'rgba(59,130,246,0.12)' : hovered ? 'rgba(255,255,255,0.04)' : undefined,
          outline: selected ? '1px solid rgba(59,130,246,0.3)' : undefined,
        }}
      >
        <FileText size={13} style={{ color: '#4ade80', flexShrink: 0 }} />
        <span style={{ fontFamily: 'monospace', fontSize: 12, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>
          {node.label}
        </span>
        {node.size != null && (
          <span style={{ fontSize: 11, color: 'var(--holo-text-faint)', flexShrink: 0 }}>
            {formatBytes(node.size)}
          </span>
        )}
        {(hovered || selected) && (
          <div style={{ display: 'flex', gap: 2, alignItems: 'center', flexShrink: 0 }}>
            <button
              onClick={(e) => { e.stopPropagation(); doDownload() }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(96,165,250,0.7)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
              title="Download"
            >
              <Download size={12} />
            </button>
            <button
              onClick={(e) => { e.stopPropagation(); doCopy() }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(96,165,250,0.7)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
              title="Copy link"
            >
              <Link size={12} />
            </button>
            {showDelete && onDelete && (
              <button
                onClick={(e) => { e.stopPropagation(); onDelete(node) }}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.5)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
                title="Delete"
              >
                <Trash2 size={12} />
              </button>
            )}
          </div>
        )}
      </div>
    )
  }

  // Folder row
  const hasKids = !!(node.children && node.children.length > 0)
  const folded = collapsed[node.path] !== false

  return (
    <div>
      <div
        style={{
          ...S.treeRow(depth),
          ...(hasKids ? S.treeFolder : {}),
          background: hovered ? 'rgba(255,255,255,0.04)' : undefined,
        }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onClick={() => hasKids && toggle(node.path)}
        onKeyDown={(e) => {
          if (hasKids && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault()
            toggle(node.path)
          }
        }}
        role={hasKids ? 'button' : undefined}
        tabIndex={hasKids ? 0 : undefined}
      >
        {hasKids ? (
          folded ? (
            <ChevronRight size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
          ) : (
            <ChevronDown size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
          )
        ) : (
          <span style={{ width: 14, flexShrink: 0 }} />
        )}
        <FolderOpen size={14} style={{ color: '#60a5fa', flexShrink: 0 }} />
        <span style={{ fontWeight: depth === 0 ? 600 : 500, flex: 1 }}>{node.label}</span>
        {hovered && showDelete && onDelete && (
          <button
            onClick={(e) => { e.stopPropagation(); onDelete(node) }}
            style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.5)', padding: '2px 4px', display: 'flex', alignItems: 'center', flexShrink: 0 }}
            title={`Delete folder ${node.label}`}
          >
            <Trash2 size={12} />
          </button>
        )}
      </div>
      {hasKids && !folded && node.children!.map((ch) => (
        <RawTreeRows
          key={ch.path}
          node={ch}
          depth={depth + 1}
          collapsed={collapsed}
          toggle={toggle}
          selectedPath={selectedPath}
          onSelectFile={onSelectFile}
          showDelete={showDelete}
          onDelete={onDelete}
          repoName={repoName}
        />
      ))}
    </div>
  )
}

// Depth-first walk that returns [leaf, ...ancestors] when a node matches `want`, else null.
// `ancestors` excludes the root (unnamed) and excludes the leaf itself.
function findWithAncestors<N extends { children?: N[] }>(
  root: N,
  want: (n: N) => boolean,
  trail: N[] = [],
): { leaf: N; ancestors: N[] } | null {
  if (want(root)) return { leaf: root, ancestors: trail }
  if (!root.children) return null
  const next = [...trail, root]
  for (const ch of root.children) {
    const hit = findWithAncestors(ch, want, next)
    if (hit) return hit
  }
  return null
}

export default function BrowsePage() {
  const [searchParams] = useSearchParams()
  const [repoName, setRepoName] = useState(searchParams.get('repo') ?? '')
  const highlightAssetPath = searchParams.get('asset') ?? ''
  const highlightComponentId = searchParams.get('cid') ?? ''
  const highlightRowRef = useRef<HTMLDivElement | null>(null)
  // Tracks which (repo, cid) pair we've already auto-drilled, so the effect
  // fires once per navigation even as tree data arrives asynchronously.
  const drilledRef = useRef<string>('')
  const [page, setPage] = useState(0)
  const [treeCollapsed, setTreeCollapsed] = useState<Record<string, boolean>>({})
  const [dockerSelection, setDockerSelection] = useState<DockerLeafSelection | null>(null)
  const [rawSelection, setRawSelection] = useState<RawFileSelection | null>(null)
  const [uploadOpen, setUploadOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{
    path: string; repo: string;
    dockerImage?: string; dockerRef?: string;
    label?: string;
    affectedPaths?: string[];
  } | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const limit = 25

  const { isAdmin } = useAuthStore()
  const queryClient = useQueryClient()

  const { data: myPrivs = [] } = useQuery<Privilege[]>({
    queryKey: ['me-privileges'],
    queryFn: () => nexspenceApi.myPrivileges(),
  })

  const canDeleteRepo = isAdmin() || myPrivs.some(p =>
    (p.attrs?.actions as string[] | undefined)?.includes('delete')
  )

  async function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    setDeleteError(null)
    try {
      if (deleteTarget.dockerImage && deleteTarget.dockerRef) {
        await nexspenceApi.deleteDockerTag(deleteTarget.repo, deleteTarget.dockerImage, deleteTarget.dockerRef)
      } else if (deleteTarget.dockerImage) {
        await nexspenceApi.deleteDockerImage(deleteTarget.repo, deleteTarget.dockerImage)
      } else {
        await nexspenceApi.deleteByPath(deleteTarget.repo, deleteTarget.path)
      }
      const repo = deleteTarget.repo
      setDeleteTarget(null)
      void queryClient.invalidateQueries({ queryKey: ['components', repo] })
      void queryClient.invalidateQueries({ queryKey: ['dockerBrowseTree', repo] })
      void queryClient.invalidateQueries({ queryKey: ['rawBrowseTree', repo] })
    } catch (err: unknown) {
      const msg = axios.isAxiosError(err) ? err.response?.data?.message ?? err.message : String(err)
      setDeleteError(msg)
    } finally {
      setDeleting(false)
    }
  }

  const { data: repos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then((r) => r.data),
  })

  const repoOptions: SelectOption[] = (repos ?? []).map(r => ({
    value: r.name,
    label: r.name,
    badge: (
      <span style={{
        fontSize: 10, fontWeight: 600, padding: '1px 6px', borderRadius: 3,
        background: (FORMAT_COLORS[r.format] ?? '#6b7280') + '22',
        color: FORMAT_COLORS[r.format] ?? '#6b7280',
        flexShrink: 0,
      }}>
        {r.format}
      </span>
    ),
    tag: (
      <span style={{ fontSize: 10, color: 'var(--holo-text-faint)', flexShrink: 0 }}>
        {r.type}
      </span>
    ),
  }))

  const selectedRepo = useMemo(() => repos.find((r) => r.name === repoName), [repos, repoName])
  const isDocker = selectedRepo?.format?.toLowerCase() === 'docker'
  const isRaw = selectedRepo?.format?.toLowerCase() === 'raw'

  const { data: components, isLoading, isError, error: componentsError, refetch } = useQuery({
    queryKey: ['components', repoName, page],
    queryFn: () =>
      apiClient
        .get('/service/rest/v1/components', {
          params: { repository: repoName, limit, offset: page * limit },
        })
        .then((r) => r.data as { items: Component[]; continuationToken: string | null }),
    enabled: !!repoName && !isDocker && !isRaw,
    retry: (failureCount, err: unknown) => {
      const status = (err as { response?: { status?: number } })?.response?.status
      if (status === 403) return false
      return failureCount < 2
    },
  })

  const {
    data: dockerTree,
    isLoading: dockerTreeLoading,
    refetch: refetchDockerTree,
  } = useQuery({
    queryKey: ['dockerBrowseTree', repoName],
    queryFn: () =>
      nexspenceApi.getDockerBrowseTree(repoName).then((r) => r.data as { root: DockerTreeNode }),
    enabled: !!repoName && isDocker,
  })

  const {
    data: rawTree,
    isLoading: rawTreeLoading,
    refetch: refetchRawTree,
  } = useQuery({
    queryKey: ['rawBrowseTree', repoName],
    queryFn: () =>
      nexspenceApi.getRawBrowseTree(repoName).then((r) => r.data as { root: RawTreeNode }),
    enabled: !!repoName && isRaw,
  })

  const { data: dockerDetail, isLoading: dockerDetailLoading } = useQuery({
    queryKey: ['dockerComponentDetail', dockerSelection?.componentId],
    queryFn: () =>
      nexusApi.getComponent(dockerSelection!.componentId).then((r) => r.data as DockerComponentDetail),
    enabled: !!repoName && isDocker && !!dockerSelection?.componentId,
  })

  const toggleTree = useCallback((p: string) => {
    setTreeCollapsed((prev) => ({ ...prev, [p]: prev[p] === false }))
  }, [])

  const onSelectDockerLeaf = useCallback((node: DockerTreeNode) => {
    if (!node.componentId) return
    setDockerSelection({
      path: node.path,
      kind: node.kind,
      componentId: node.componentId,
      imageRef: node.imageRef,
      version: node.version ?? node.label,
    })
  }, [])

  const items = components?.items ?? []
  const hasNext = !!components?.continuationToken

  // When arriving from Search with ?asset=/?cid=, scroll the matching row into view.
  useEffect(() => {
    if (!highlightAssetPath && !highlightComponentId) return
    if (!highlightRowRef.current) return
    highlightRowRef.current.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [highlightAssetPath, highlightComponentId, items])

  // Auto-drill Docker tree: walk to leaf with matching componentId, expand ancestors, select it.
  useEffect(() => {
    if (!highlightComponentId || !isDocker || !dockerTree?.root) return
    const key = `docker:${repoName}:${highlightComponentId}`
    if (drilledRef.current === key) return
    const hit = findWithAncestors(
      dockerTree.root,
      (n) => n.componentId === highlightComponentId,
    )
    if (!hit) return
    drilledRef.current = key
    setTreeCollapsed((prev) => {
      const next = { ...prev }
      for (const a of hit.ancestors) if (a.path) next[a.path] = false
      return next
    })
    if (hit.leaf.componentId) {
      setDockerSelection({
        path: hit.leaf.path,
        kind: hit.leaf.kind,
        componentId: hit.leaf.componentId,
        imageRef: hit.leaf.imageRef,
        version: hit.leaf.version ?? hit.leaf.label,
      })
    }
  }, [highlightComponentId, isDocker, dockerTree, repoName])

  // Auto-drill Raw tree by componentId.
  useEffect(() => {
    if (!highlightComponentId || !isRaw || !rawTree?.root) return
    const key = `raw:${repoName}:${highlightComponentId}`
    if (drilledRef.current === key) return
    const hit = findWithAncestors(
      rawTree.root,
      (n) => n.componentId === highlightComponentId,
    )
    if (!hit) return
    drilledRef.current = key
    setTreeCollapsed((prev) => {
      const next = { ...prev }
      for (const a of hit.ancestors) if (a.path) next[a.path] = false
      return next
    })
    setRawSelection({ path: hit.leaf.path, node: hit.leaf })
  }, [highlightComponentId, isRaw, rawTree, repoName])

  const subtitle = !repoName
    ? 'Select a repository to browse'
    : isDocker || isRaw
      ? selectedRepo!.name
      : `${items.length} components loaded`

  return (
    <div style={S.page}>
      <div style={{ padding: '24px 24px 0', marginBottom: 16 }}>
        <div className="holo-section-label" style={{ marginBottom: 6 }}>WORKSPACE / BROWSE</div>
        <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
          <HoloText>Browse</HoloText>
        </h1>
        <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Explore repository contents</p>
      </div>
      <div style={S.header}>
        <p style={S.subtitle}>{subtitle}</p>
        {repoName && (
          <button
            style={S.iconBtn}
            onClick={() => isDocker ? refetchDockerTree() : isRaw ? refetchRawTree() : refetch()}
            title="Refresh"
          >
            <RefreshCw size={16} />
          </button>
        )}
      </div>

      <div style={S.toolbar}>
        <Select
          options={repoOptions}
          value={repoName}
          onChange={(name) => {
            setRepoName(name)
            setPage(0)
            setTreeCollapsed({})
            setDockerSelection(null)
            setRawSelection(null)
            setUploadOpen(false)
          }}
          placeholder="— Select repository —"
          style={{ minWidth: 240 }}
        />
        {isRaw && selectedRepo?.type === 'hosted' && (isAdmin() || myPrivs.some(p =>
          (p.attrs?.actions as string[] | undefined)?.includes('write')
        )) && (
          <HoloButton variant="primary" icon={<Upload size={14} />} onClick={() => setUploadOpen(true)}>
            Upload
          </HoloButton>
        )}
      </div>

      {!repoName ? (
        <div style={S.empty}>
          <FolderOpen size={40} style={{ opacity: 0.3 }} />
          <p>Choose a repository above</p>
        </div>
      ) : isDocker ? (
        dockerTreeLoading ? (
          <div style={S.empty}>Loading tree…</div>
        ) : !dockerTree?.root?.children?.length ? (
          <div style={S.empty}>
            <Package size={40} style={{ opacity: 0.3 }} />
            <p>No Docker metadata cached yet — pull an image through this repository first</p>
          </div>
        ) : (
          <div style={S.dockerLayout}>
            <div className="holo-card" style={{ ...S.treePanel, flex: '1 1 280px', minWidth: 260, maxWidth: '100%' }}>
              <p style={S.treeHint}>
                Expand folders to browse images. Click a tag, manifest, or blob for Nexus-style asset metadata.
              </p>
              {dockerTree.root.children!.map((n) => (
                <DockerTreeRows
                  key={n.path}
                  node={n}
                  depth={0}
                  collapsed={treeCollapsed}
                  toggle={toggleTree}
                  selectedPath={dockerSelection?.path ?? null}
                  onSelectLeaf={onSelectDockerLeaf}
                  showDelete={canDeleteRepo}
                  onDelete={node => {
                    if (node.kind === 'tag') {
                      setDeleteTarget({
                        path: `/manifests/${node.imageRef}/${node.version ?? node.label}`,
                        repo: repoName,
                        dockerImage: node.imageRef,
                        dockerRef: node.version ?? node.label,
                        label: `${node.imageRef}:${node.version ?? node.label}`,
                      })
                    } else {
                      // folder node — path is like /da/devops/python, strip leading slash
                      const imagePath = node.path.replace(/^\//, '')
                      setDeleteTarget({
                        path: node.path,
                        repo: repoName,
                        dockerImage: imagePath,
                        label: imagePath,
                      })
                    }
                  }}
                />
              ))}
            </div>
            <div className="holo-card" style={S.detailPanel}>
              <h2 style={S.detailTitle}>Component details</h2>
              {!dockerSelection ? (
                <p style={S.muted}>Select a tag, manifest, or blob in the tree.</p>
              ) : dockerDetailLoading ? (
                <p style={S.muted}>Loading…</p>
              ) : dockerDetail ? (
                <DockerBrowseDetailBody comp={dockerDetail} sel={dockerSelection} />
              ) : (
                <p style={S.muted}>Could not load component.</p>
              )}
            </div>
          </div>
        )
      ) : isRaw ? (
        rawTreeLoading ? (
          <div style={S.empty}>Loading tree…</div>
        ) : !rawTree?.root?.children?.length ? (
          <div style={S.empty}>
            <Package size={40} style={{ opacity: 0.3 }} />
            <p>No files in this repository yet</p>
          </div>
        ) : (
          <div style={S.dockerLayout}>
            <div className="holo-card" style={{ ...S.treePanel, flex: '1 1 280px', minWidth: 260, maxWidth: '100%' }}>
              <p style={S.treeHint}>
                Expand folders to browse. Click a file for details.
              </p>
              {rawTree.root.children!.map((n) => (
                <RawTreeRows
                  key={n.path}
                  node={n}
                  depth={0}
                  collapsed={treeCollapsed}
                  toggle={toggleTree}
                  selectedPath={rawSelection?.path ?? null}
                  onSelectFile={(node) => setRawSelection({ path: node.path, node })}
                  showDelete={canDeleteRepo}
                  onDelete={(node) => {
                    if (node.kind === 'file') {
                      setDeleteTarget({ path: node.path, repo: repoName, label: node.path })
                    } else {
                      const paths = collectLeafPaths(node)
                      setDeleteTarget({ path: node.path, repo: repoName, label: node.path, affectedPaths: paths })
                    }
                  }}
                  repoName={repoName}
                />
              ))}
            </div>
            <div className="holo-card" style={S.detailPanel}>
              <h2 style={S.detailTitle}>File details</h2>
              {rawSelection ? (() => {
                const node = rawSelection.node
                const cleanPath = node.path.replace(/^\//, '')
                const downloadUrl = `/repository/${repoName}/${cleanPath}`
                const copyUrl = `${window.location.origin}/repository/${repoName}/${cleanPath}`
                return (
                  <>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Name</div>
                      <div style={S.detailValue}>{node.label}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Path</div>
                      <div style={{ ...S.detailValue, fontFamily: 'monospace', color: '#93c5fd' }}>{node.path}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Content type</div>
                      <div style={S.detailValue}>{node.contentType || '—'}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Size</div>
                      <div style={S.detailValue}>{formatBytes(node.size ?? 0)}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>SHA256</div>
                      <div style={{ ...S.detailValue, fontFamily: 'monospace', fontSize: 10, color: '#93c5fd' }}>{node.sha256 || '—'}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Uploaded</div>
                      <div style={S.detailValue}>{formatDateTime(node.updatedAt)}</div>
                    </div>
                    <div style={S.detailRow}>
                      <div style={S.detailLabel}>Repository</div>
                      <div style={S.detailValue}>{repoName}</div>
                    </div>
                    <div style={S.detailActions}>
                      <button
                        style={S.btnDl}
                        onClick={() => {
                          void apiClient.get(downloadUrl, { responseType: 'blob' }).then((res) => {
                            const url = window.URL.createObjectURL(res.data as Blob)
                            const a = document.createElement('a')
                            a.href = url
                            a.download = node.label
                            document.body.appendChild(a)
                            a.click()
                            document.body.removeChild(a)
                            window.URL.revokeObjectURL(url)
                          })
                        }}
                      >
                        <Download size={13} /> Download
                      </button>
                      <button
                        style={S.btnCopy}
                        onClick={() => { void navigator.clipboard.writeText(copyUrl) }}
                      >
                        <Link size={13} /> Copy link
                      </button>
                    </div>
                  </>
                )
              })() : (
                <p style={S.muted}>Select a file in the tree.</p>
              )}
            </div>
          </div>
        )
      ) : isError && (componentsError as { response?: { status?: number } })?.response?.status === 403 ? (
        <div style={S.empty}>
          <Package size={40} style={{ opacity: 0.3 }} />
          <p style={{ color: '#ef4444' }}>Access denied — you don't have permission to browse this repository.</p>
        </div>
      ) : isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : items.length === 0 ? (
        <div style={S.empty}>
          <Package size={40} style={{ opacity: 0.3 }} />
          <p>No components in this repository</p>
        </div>
      ) : (
        <>
          <div className="holo-card" style={S.table}>
            <div style={S.thead}>
              <div>Name</div>
              <div>Group</div>
              <div>Version</div>
              <div>Format</div>
              <div>Assets</div>
              <div />
            </div>
            {items.map((c) => {
              const color = FORMAT_COLORS[c.format] ?? '#6b7280'
              const firstAsset = c.assets?.[0]
              const assetPath = firstAsset?.path ?? c.name
              const isHighlighted = (!!highlightComponentId && c.id === highlightComponentId) ||
                (!!highlightAssetPath && !!c.assets?.some((a) => a.path === highlightAssetPath))
              return (
                <div
                  key={c.id}
                  ref={isHighlighted ? highlightRowRef : undefined}
                  style={{
                    ...S.trow,
                    ...(isHighlighted
                      ? { outline: '1px solid rgba(59,130,246,0.6)', background: 'rgba(59,130,246,0.08)' }
                      : {}),
                  }}
                >
                  <div style={{ fontWeight: 600, color: 'var(--holo-text)' }}>{c.name}</div>
                  <div style={S.muted}>{c.group || '—'}</div>
                  <div>{c.version}</div>
                  <div>
                    <span style={S.badge(color)}>{c.format}</span>
                  </div>
                  <div style={S.path}>
                    {firstAsset
                      ? `${firstAsset.path}${c.assets!.length > 1 ? ` +${c.assets!.length - 1}` : ''}`
                      : '—'}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    {canDeleteRepo && (
                      <button
                        onClick={() => setDeleteTarget({ path: assetPath, repo: repoName })}
                        style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.6)', padding: '2px 4px', display: 'flex', alignItems: 'center' }}
                        title="Delete"
                      >
                        <Trash2 size={13} />
                      </button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>

          <div style={S.pager}>
            <button style={S.pgBtn(page === 0)} disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
              ← Prev
            </button>
            <span style={S.muted}>Page {page + 1}</span>
            <button style={S.pgBtn(!hasNext)} disabled={!hasNext} onClick={() => setPage((p) => p + 1)}>
              Next →
            </button>
          </div>
        </>
      )}

      <HoloModal open={!!deleteTarget} onClose={() => { setDeleteTarget(null); setDeleteError(null) }}>
          {deleteTarget && <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 8 }}>
              <Trash2 size={17} style={{ color: '#ef4444' }} />
              {deleteTarget.affectedPaths ? 'Delete folder?' : 'Delete file?'}
            </h3>
            <div style={{ fontSize: 13, color: 'var(--holo-text-dim)' }}>
              <span style={{ fontFamily: 'monospace', color: '#fca5a5', fontSize: 12 }}>{deleteTarget.label ?? deleteTarget.path}</span>
              {deleteTarget.affectedPaths && (
                <p style={{ margin: '8px 0 0', fontSize: 12, color: 'var(--holo-text-faint)' }}>
                  All files in this folder will be permanently deleted:
                </p>
              )}
            </div>
            {deleteTarget.affectedPaths && (
              <div style={{
                background: 'rgba(239,68,68,0.05)',
                border: '1px solid rgba(239,68,68,0.15)',
                borderRadius: 8,
                padding: '10px 12px',
                fontSize: 11,
                fontFamily: 'monospace',
                color: 'var(--holo-text-dim)',
                maxHeight: 120,
                overflowY: 'auto' as const,
                display: 'flex',
                flexDirection: 'column' as const,
                gap: 3,
              }}>
                <div style={{ fontSize: 11, color: 'rgba(239,68,68,0.7)', fontFamily: 'system-ui', fontWeight: 600, marginBottom: 4 }}>
                  {deleteTarget.affectedPaths.length} files affected
                </div>
                {deleteTarget.affectedPaths.map((p) => (
                  <span key={p}>{p}</span>
                ))}
              </div>
            )}
            <p style={{ margin: 0, fontSize: 12, color: 'var(--holo-text-faint)' }}>
              This action cannot be undone.
            </p>
            {deleteError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{deleteError}</div>
            )}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <HoloButton onClick={() => { setDeleteTarget(null); setDeleteError(null) }} disabled={deleting}>
                Cancel
              </HoloButton>
              <HoloButton variant="danger" onClick={confirmDelete} disabled={deleting}>
                {deleting ? 'Deleting…' : deleteTarget.affectedPaths ? `Delete ${deleteTarget.affectedPaths.length} files` : 'Delete'}
              </HoloButton>
            </div>
          </div>}
      </HoloModal>

      {uploadOpen && isRaw && (
        <RawUploadModal
          repoName={repoName}
          onClose={() => setUploadOpen(false)}
          onSuccess={() => {
            void queryClient.invalidateQueries({ queryKey: ['rawBrowseTree', repoName] })
            setUploadOpen(false)
          }}
        />
      )}
    </div>
  )
}

function RawUploadModal({
  repoName,
  onClose,
  onSuccess,
}: {
  repoName: string
  onClose: () => void
  onSuccess: () => void
}) {
  const [file, setFile] = useState<File | null>(null)
  const [destPath, setDestPath] = useState('')
  const [uploadState, setUploadState] = useState<'idle' | 'uploading' | 'done' | 'error'>('idle')
  const [progress, setProgress] = useState(0)
  const [uploadError, setUploadError] = useState<string | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const xhrRef = useState<XMLHttpRequest | null>(null)

  function handleFileChange(f: File) {
    setFile(f)
    setDestPath(prev => {
      const lastSlash = prev.lastIndexOf('/')
      const dir = lastSlash >= 0 ? prev.slice(0, lastSlash + 1) : ''
      return dir + f.name
    })
  }

  function doUpload() {
    if (!file) return
    const xhr = new XMLHttpRequest()
    xhrRef[0] = xhr
    const path = destPath.replace(/^\//, '')
    xhr.open('PUT', `/repository/${repoName}/${path}`)
    xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream')
    const token = localStorage.getItem('nexspence_token')
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status === 201 || xhr.status === 200 || xhr.status === 204) {
        setUploadState('done')
      } else {
        setUploadState('error')
        setUploadError(xhr.responseText || `HTTP ${xhr.status}`)
      }
    }
    xhr.onerror = () => {
      setUploadState('error')
      setUploadError('Network error')
    }
    xhr.send(file)
    setUploadState('uploading')
    setProgress(0)
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(false)
    const f = e.dataTransfer.files[0]
    if (f) handleFileChange(f)
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={{ width: 520, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div>
          <div style={{ fontSize: 16, fontWeight: 700, color: 'var(--holo-text)' }}>Upload file</div>
          <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', marginTop: 2 }}>→ {repoName}{destPath ? ' / ' + destPath : ''}</div>
        </div>

        {/* Drop zone */}
        <div
          style={{
            border: `2px dashed ${dragOver ? 'rgba(59,130,246,0.7)' : file ? 'rgba(59,130,246,0.6)' : 'rgba(59,130,246,0.4)'}`,
            borderRadius: 10,
            padding: '24px 16px',
            textAlign: 'center' as const,
            background: file || dragOver ? 'rgba(59,130,246,0.08)' : 'rgba(59,130,246,0.04)',
            cursor: 'pointer',
          }}
          onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
          onDragLeave={() => setDragOver(false)}
          onDrop={handleDrop}
          onClick={() => {
            const inp = document.createElement('input')
            inp.type = 'file'
            inp.onchange = () => { if (inp.files?.[0]) handleFileChange(inp.files[0]) }
            inp.click()
          }}
        >
          {file ? (
            <>
              <div style={{ fontSize: 28, marginBottom: 8 }}>📦</div>
              <div style={{ fontSize: 14, fontWeight: 600, color: '#93c5fd' }}>{file.name}</div>
              <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 3 }}>
                {formatBytes(file.size)} · {file.type || 'application/octet-stream'}
              </div>
              <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', marginTop: 6 }}>
                {uploadState === 'idle' ? 'Click or drag to replace file' : ''}
              </div>
            </>
          ) : (
            <>
              <div style={{ fontSize: 28, marginBottom: 8 }}>📂</div>
              <div style={{ fontSize: 14, color: 'var(--holo-text-dim)' }}>Click or drag a file here</div>
            </>
          )}
        </div>

        {/* Path field */}
        {uploadState !== 'done' && (
          <div>
            <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', letterSpacing: '.05em', textTransform: 'uppercase' as const, marginBottom: 6 }}>
              Destination path
            </div>
            <HoloInput
              type="text"
              value={destPath}
              onChange={(e) => setDestPath(e.target.value)}
              disabled={uploadState === 'uploading'}
              placeholder="e.g. releases/myapp/1.0.0/myapp.tar.gz"
              style={{ width: '100%', fontSize: 12, fontFamily: 'monospace', boxSizing: 'border-box' as const }}
            />
          </div>
        )}

        {/* Progress bar */}
        {uploadState === 'uploading' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <div style={{ height: 4, background: 'rgba(255,255,255,0.08)', borderRadius: 2, overflow: 'hidden' }}>
              <div style={{ height: '100%', background: 'linear-gradient(90deg, #3b82f6, #60a5fa)', borderRadius: 2, width: `${progress}%`, transition: 'width .3s' }} />
            </div>
            <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'flex', justifyContent: 'space-between' }}>
              <span>Uploading…</span>
              <span>{progress}%</span>
            </div>
          </div>
        )}

        {/* Success */}
        {uploadState === 'done' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', background: 'rgba(34,197,94,0.1)', border: '1px solid rgba(34,197,94,0.3)', borderRadius: 8, fontSize: 13, color: '#86efac' }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#86efac" strokeWidth="2.5"><polyline points="20 6 9 17 4 12"/></svg>
            File uploaded successfully
          </div>
        )}

        {/* Error */}
        {uploadState === 'error' && uploadError && (
          <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>
            {uploadError}
          </div>
        )}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <HoloButton
            onClick={() => {
              if (uploadState === 'uploading' && xhrRef[0]) {
                xhrRef[0].abort()
                setUploadState('idle')
              } else {
                onClose()
              }
            }}
          >
            Cancel
          </HoloButton>
          {uploadState === 'done' ? (
            <HoloButton variant="primary" onClick={() => { onSuccess() }}>
              Done
            </HoloButton>
          ) : (
            <HoloButton
              variant="primary"
              disabled={!file || uploadState === 'uploading'}
              onClick={doUpload}
            >
              {uploadState === 'uploading' ? 'Uploading…' : 'Upload'}
            </HoloButton>
          )}
        </div>
      </div>
    </HoloModal>
  )
}
