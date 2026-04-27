import React, { useState } from 'react'

export interface WizardStep {
  label: string
  content: React.ReactNode
}

export interface WizardProps {
  steps: WizardStep[]
  onFinish: () => void | Promise<void>
  finishLabel?: string
  onValidateStep?: (stepIndex: number) => boolean | Promise<boolean>
  onClose: () => void
  loading?: boolean
  error?: string
}

export function Wizard({
  steps,
  onFinish,
  finishLabel = 'Create',
  onValidateStep,
  onClose,
  loading,
  error,
}: WizardProps) {
  const [step, setStep] = useState(0)
  const [sliding, setSliding] = useState<'left' | 'right' | null>(null)
  const total = steps.length

  const goTo = (dir: 'left' | 'right', target: number) => {
    setSliding(dir)
    setTimeout(() => {
      setStep(target)
      setSliding(null)
    }, 200)
  }

  const handleNext = async () => {
    if (onValidateStep) {
      const ok = await onValidateStep(step)
      if (!ok) return
    }
    if (step < total - 1) {
      goTo('left', step + 1)
    } else {
      await onFinish()
    }
  }

  const handleBack = () => {
    if (step > 0) goTo('right', step - 1)
  }

  return (
    <div className="holo-overlay" onClick={onClose}>
      <div className="holo-wizard" onClick={e => e.stopPropagation()}>
        <div className="holo-wizard__progress">
          {steps.map((s, i) => (
            <React.Fragment key={i}>
              <div
                className="holo-wizard__step-meta"
                onClick={() => { if (i < step) goTo('right', i) }}
                style={{ cursor: i < step ? 'pointer' : 'default' }}
              >
                <div className={`holo-wizard__dot holo-wizard__dot--${i < step ? 'done' : i === step ? 'active' : 'pending'}`}>
                  {i < step ? '✓' : i + 1}
                </div>
                <div className="holo-wizard__step-text">
                  <span className="holo-wizard__step-num">Step {i + 1}</span>
                  <span className={`holo-wizard__step-name holo-wizard__step-name--${i < step ? 'done' : i === step ? 'active' : 'pending'}`}>
                    {s.label}
                  </span>
                </div>
              </div>
              {i < total - 1 && (
                <div className={`holo-wizard__line${i < step ? ' holo-wizard__line--done' : ''}`} />
              )}
            </React.Fragment>
          ))}
        </div>

        <div className={`holo-wizard__body${sliding ? ` holo-wizard__body--${sliding}` : ''}`}>
          {steps[step].content}
        </div>

        {error && <div className="holo-wizard__error">{error}</div>}

        <div className="holo-wizard__footer">
          <button
            type="button"
            className="holo-btn holo-wizard__back"
            onClick={handleBack}
            style={{ visibility: step > 0 ? 'visible' : 'hidden' }}
          >
            ← Back
          </button>
          <span className="holo-wizard__step-info">Step {step + 1} of {total}</span>
          <button
            type="button"
            className="holo-btn holo-btn--primary"
            onClick={handleNext}
            disabled={!!loading}
          >
            {loading ? 'Loading…' : step === total - 1 ? finishLabel : 'Next →'}
          </button>
        </div>
      </div>
    </div>
  )
}
