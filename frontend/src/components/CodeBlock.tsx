import { useEffect, useRef, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import styles from './CodeBlock.module.css'

/** A code snippet with its language tag and a copy-to-clipboard button. */
export function CodeBlock({ lang, content, copyLabel }: { lang: string; content: string; copyLabel?: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (timerRef.current) clearTimeout(timerRef.current) }, [])
  const copy = () => {
    void navigator.clipboard.writeText(content).then(() => {
      setCopied(true)
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <div className={styles.codeBlock}>
      <div className={styles.codeHeader}>
        <span className={styles.codeLang}>{lang}</span>
        <button
          type="button"
          className={`${styles.copyBtn} ${copied ? styles.copied : ''}`}
          onClick={copy}
          aria-label={copyLabel}
        >
          {copied ? <Check size={11} /> : <Copy size={11} />}
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <div className={styles.codeBody}>
        <pre>{content}</pre>
      </div>
    </div>
  )
}
