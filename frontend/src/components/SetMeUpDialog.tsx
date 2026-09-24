import { useEffect, useId, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { Terminal, X } from 'lucide-react'
import { HoloButton, HoloInput, HoloModal } from '@/components/holo'
import { CodeBlock } from '@/components/CodeBlock'
import { buildSetupGuide, type SetupSection } from '@/setup/snippets'
import styles from './SetMeUpDialog.module.css'

export interface SetMeUpRepo {
  name: string
  format: string
  type: string
}

/** Where client snippets point: the server's own origin, as the browser sees it. */
function currentBaseUrl(): string {
  return window.location.origin
}

/**
 * Set Me Up: per-repository client configuration, one tab per client of the
 * repository's format, with this repository's URL filled in (#534).
 *
 * Renders nothing when `repo` is null, so callers can keep it mounted.
 */
export function SetMeUpDialog({ repo, onClose }: { repo: SetMeUpRepo | null; onClose: () => void }) {
  if (!repo) return null
  // Keyed by repository so switching repos starts from the first tab.
  return <SetMeUpContent key={`${repo.name}:${repo.format}:${repo.type}`} repo={repo} onClose={onClose} />
}

function SetMeUpContent({ repo, onClose }: { repo: SetMeUpRepo; onClose: () => void }) {
  const [username, setUsername] = useState('')
  const guide = useMemo(() => buildSetupGuide({
    format: repo.format,
    repoName: repo.name,
    repoType: repo.type,
    baseUrl: currentBaseUrl(),
    username,
  }), [repo.format, repo.name, repo.type, username])
  const [activeId, setActiveId] = useState(guide.clients[0]?.id ?? '')
  const active = guide.clients.find(c => c.id === activeId) ?? guide.clients[0]

  const baseId = useId()
  const tabRefs = useRef<Record<string, HTMLButtonElement | null>>({})
  const inputRef = useRef<HTMLInputElement>(null)

  // Escape closes; focus moves into the dialog on open and back where it was on close.
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    inputRef.current?.focus()
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onCloseRef.current()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (previous && document.contains(previous)) previous.focus()
    }
  }, [])

  const onTabKey = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const n = guide.clients.length
    let next = -1
    if (e.key === 'ArrowRight') next = (index + 1) % n
    else if (e.key === 'ArrowLeft') next = (index - 1 + n) % n
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = n - 1
    if (next < 0) return
    e.preventDefault()
    const id = guide.clients[next].id
    setActiveId(id)
    tabRefs.current[id]?.focus()
  }

  const tabId = (id: string) => `${baseId}-tab-${id}`
  const panelId = (id: string) => `${baseId}-panel-${id}`

  return (
    <HoloModal open onClose={onClose} ariaLabel={`Set me up: ${repo.name}`}>
      <div className={styles.body}>
        <div className={styles.header}>
          <Terminal size={20} className={styles.headerIcon} aria-hidden="true" />
          <div>
            <h2 className={styles.title}>Set me up</h2>
            <div className={styles.subtitle}>{repo.format} · {repo.type} · {repo.name}</div>
          </div>
          <HoloButton className={styles.close} onClick={onClose} aria-label="Close dialog" title="Close">
            <X size={16} />
          </HoloButton>
        </div>

        <CodeBlock lang="Repository URL" content={guide.repoUrl} copyLabel="Copy repository URL" />

        <div className={styles.fieldRow}>
          <label htmlFor={`${baseId}-user`} className={styles.fieldLabel}>Username</label>
          <HoloInput
            id={`${baseId}-user`}
            ref={inputRef}
            className={styles.fieldInput}
            value={username}
            onChange={e => setUsername(e.target.value.trim())}
            placeholder="optional"
            autoComplete="off"
            spellCheck={false}
          />
          <p className={styles.hint}>
            Fills the username into the snippets. Passwords stay placeholders — use an API token from your profile.
          </p>
        </div>

        {guide.note && <div className={styles.note} role="note">{guide.note}</div>}

        <div className={`holo-tabs ${styles.tabs}`} role="tablist" aria-label="Clients">
          {guide.clients.map((c, i) => {
            const selected = c.id === active?.id
            return (
              <button
                key={c.id}
                ref={el => { tabRefs.current[c.id] = el }}
                id={tabId(c.id)}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-controls={panelId(c.id)}
                tabIndex={selected ? 0 : -1}
                className={`holo-tab ${selected ? 'active' : ''}`}
                onClick={() => setActiveId(c.id)}
                onKeyDown={e => onTabKey(e, i)}
              >
                {c.label}
              </button>
            )
          })}
        </div>

        {active && (
          <div
            className={styles.panel}
            role="tabpanel"
            id={panelId(active.id)}
            aria-labelledby={tabId(active.id)}
            tabIndex={0}
          >
            {active.sections.map((s, i) => <Section key={i} section={s} />)}
          </div>
        )}

        <div className={styles.footer}>
          <HoloButton onClick={onClose}>Close</HoloButton>
        </div>
      </div>
    </HoloModal>
  )
}

function Section({ section }: { section: SetupSection }) {
  return (
    <div>
      <p className={styles.sectionTitle}>{section.title}</p>
      {section.text && <p className={styles.sectionText}>{section.text}</p>}
      {section.note && <div className={styles.note}>{section.note}</div>}
      {section.codes.map((c, i) => (
        <div key={i}>
          {c.label && <p className={styles.codeLabel}>{c.label}</p>}
          <CodeBlock lang={c.lang} content={c.content} copyLabel={`Copy ${c.label ?? section.title}`} />
        </div>
      ))}
    </div>
  )
}
