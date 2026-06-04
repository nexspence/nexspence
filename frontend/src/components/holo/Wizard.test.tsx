import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Wizard, WizardStep } from './Wizard'

const steps: WizardStep[] = [
  { label: 'First', content: <div>Step one body</div> },
  { label: 'Second', content: <div>Step two body</div> },
  { label: 'Third', content: <div>Step three body</div> },
]

describe('Wizard', () => {
  it('renders the first step and step indicator', () => {
    render(<Wizard steps={steps} onFinish={() => {}} onClose={() => {}} />)
    expect(screen.getByText('Step one body')).toBeInTheDocument()
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument()
    // Next button (not finish on first step).
    expect(screen.getByRole('button', { name: 'Next →' })).toBeInTheDocument()
  })

  it('advances through steps with Next and back with Back', async () => {
    render(<Wizard steps={steps} onFinish={() => {}} onClose={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'Next →' }))
    await waitFor(() => expect(screen.getByText('Step two body')).toBeInTheDocument())
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '← Back' }))
    await waitFor(() => expect(screen.getByText('Step one body')).toBeInTheDocument())
  })

  it('blocks Next when validation fails', async () => {
    const onValidateStep = vi.fn().mockResolvedValue(false)
    render(<Wizard steps={steps} onFinish={() => {}} onClose={() => {}} onValidateStep={onValidateStep} />)
    await userEvent.click(screen.getByRole('button', { name: 'Next →' }))
    expect(onValidateStep).toHaveBeenCalledWith(0)
    // Still on step 1.
    expect(screen.getByText('Step one body')).toBeInTheDocument()
  })

  it('calls onFinish on the last step with a custom finishLabel', async () => {
    const onFinish = vi.fn()
    const onValidateStep = vi.fn().mockResolvedValue(true)
    render(
      <Wizard
        steps={steps}
        onFinish={onFinish}
        onClose={() => {}}
        onValidateStep={onValidateStep}
        finishLabel="Save it"
      />
    )
    await userEvent.click(screen.getByRole('button', { name: 'Next →' }))
    await waitFor(() => expect(screen.getByText('Step two body')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Next →' }))
    await waitFor(() => expect(screen.getByText('Step three body')).toBeInTheDocument())
    const finishBtn = screen.getByRole('button', { name: 'Save it' })
    await userEvent.click(finishBtn)
    expect(onFinish).toHaveBeenCalledOnce()
  })

  it('renders the error message and a loading state', () => {
    render(<Wizard steps={steps} onFinish={() => {}} onClose={() => {}} error="Bad input" loading />)
    expect(screen.getByText('Bad input')).toBeInTheDocument()
    const btn = screen.getByRole('button', { name: 'Loading…' })
    expect(btn).toBeDisabled()
  })

  it('closes when clicking the overlay but not the wizard body', async () => {
    const onClose = vi.fn()
    const { container } = render(<Wizard steps={steps} onFinish={() => {}} onClose={onClose} />)
    await userEvent.click(screen.getByText('Step one body'))
    expect(onClose).not.toHaveBeenCalled()
    const overlay = container.querySelector('.holo-overlay')!
    await userEvent.click(overlay)
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('jumps back to a completed step by clicking its dot', async () => {
    const onValidateStep = vi.fn().mockResolvedValue(true)
    render(<Wizard steps={steps} onFinish={() => {}} onClose={() => {}} onValidateStep={onValidateStep} />)
    await userEvent.click(screen.getByRole('button', { name: 'Next →' }))
    await waitFor(() => expect(screen.getByText('Step two body')).toBeInTheDocument())
    // Step 1 is now "done" — its meta is clickable. Click the "First" step name.
    await userEvent.click(screen.getByText('First'))
    await waitFor(() => expect(screen.getByText('Step one body')).toBeInTheDocument())
  })
})
