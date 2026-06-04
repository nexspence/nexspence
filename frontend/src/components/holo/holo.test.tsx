import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  HoloApp, HoloText, HoloCard, TiltCard, HoloButton, HoloPill,
  HoloInput, HoloTabs, CountUp, HoloModal,
} from './holo'

describe('HoloApp', () => {
  it('renders children and merges className', () => {
    render(<HoloApp className="extra"><span>shell</span></HoloApp>)
    const el = screen.getByText('shell').parentElement!
    expect(el).toHaveClass('holo-app')
    expect(el).toHaveClass('extra')
  })
})

describe('HoloText', () => {
  it('renders as a span by default', () => {
    render(<HoloText>label</HoloText>)
    const el = screen.getByText('label')
    expect(el.tagName).toBe('SPAN')
    expect(el).toHaveClass('holo-text')
  })

  it('renders as a custom tag via `as`', () => {
    render(<HoloText as="h2">heading</HoloText>)
    expect(screen.getByText('heading').tagName).toBe('H2')
  })
})

describe('HoloCard', () => {
  it('renders children', () => {
    render(<HoloCard>body</HoloCard>)
    expect(screen.getByText('body')).toHaveClass('holo-card')
  })

  it('applies accent class and renders edge element', () => {
    const { container } = render(<HoloCard accent edge>x</HoloCard>)
    const card = container.querySelector('.holo-card')!
    expect(card).toHaveClass('holo-card--accent')
    expect(card.querySelector('.holo-card__edge')).toBeTruthy()
  })
})

describe('TiltCard', () => {
  it('updates transform on mouse move and resets on leave', () => {
    const { container } = render(<TiltCard><span>tilt</span></TiltCard>)
    const root = container.firstChild as HTMLElement
    // jsdom getBoundingClientRect returns zeros; ensure handlers don't throw.
    fireEvent.mouseMove(root, { clientX: 10, clientY: 10 })
    expect(root.style.transform).toContain('perspective')
    fireEvent.mouseLeave(root)
    expect(root.style.transform).toContain('rotateX(0deg)')
  })
})

describe('HoloButton', () => {
  it('renders default variant with type=button', () => {
    render(<HoloButton>Click</HoloButton>)
    const btn = screen.getByRole('button', { name: 'Click' })
    expect(btn).toHaveAttribute('type', 'button')
    expect(btn).toHaveClass('holo-btn')
    expect(btn.className).not.toContain('holo-btn--')
  })

  it('applies primary and danger variant classes', () => {
    const { rerender } = render(<HoloButton variant="primary">P</HoloButton>)
    expect(screen.getByRole('button')).toHaveClass('holo-btn--primary')
    rerender(<HoloButton variant="danger">D</HoloButton>)
    expect(screen.getByRole('button')).toHaveClass('holo-btn--danger')
  })

  it('renders icon and fires onClick', async () => {
    const onClick = vi.fn()
    render(<HoloButton icon={<span data-testid="ic" />} onClick={onClick}>Go</HoloButton>)
    expect(screen.getByTestId('ic')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Go' }))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('respects disabled', async () => {
    const onClick = vi.fn()
    render(<HoloButton disabled onClick={onClick}>No</HoloButton>)
    await userEvent.click(screen.getByRole('button'))
    expect(onClick).not.toHaveBeenCalled()
  })
})

describe('HoloPill', () => {
  it('renders default tone with no modifier', () => {
    render(<HoloPill>pill</HoloPill>)
    const el = screen.getByText('pill')
    expect(el).toHaveClass('holo-pill')
    expect(el.className).not.toContain('holo-pill--')
  })

  it('applies tone modifier classes', () => {
    const { rerender } = render(<HoloPill tone="success">s</HoloPill>)
    expect(screen.getByText('s')).toHaveClass('holo-pill--success')
    rerender(<HoloPill tone="warn">w</HoloPill>)
    expect(screen.getByText('w')).toHaveClass('holo-pill--warn')
    rerender(<HoloPill tone="danger">d</HoloPill>)
    expect(screen.getByText('d')).toHaveClass('holo-pill--danger')
  })
})

describe('HoloInput', () => {
  it('forwards ref and handles onChange', async () => {
    const ref = { current: null as HTMLInputElement | null }
    const onChange = vi.fn()
    render(<HoloInput ref={ref} placeholder="type here" onChange={onChange} />)
    const input = screen.getByPlaceholderText('type here')
    expect(ref.current).toBe(input)
    expect(input).toHaveClass('holo-input')
    await userEvent.type(input, 'hi')
    expect(onChange).toHaveBeenCalled()
  })
})

describe('HoloTabs', () => {
  it('marks active tab and fires onChange on click', async () => {
    const onChange = vi.fn()
    render(
      <HoloTabs
        items={[{ value: 'a', label: 'Alpha' }, { value: 'b', label: 'Beta' }]}
        value="a"
        onChange={onChange}
      />
    )
    const alpha = screen.getByRole('button', { name: 'Alpha' })
    const beta = screen.getByRole('button', { name: 'Beta' })
    expect(alpha).toHaveClass('active')
    expect(beta).not.toHaveClass('active')
    await userEvent.click(beta)
    expect(onChange).toHaveBeenCalledWith('b')
  })
})

describe('CountUp', () => {
  it('animates toward the target value', async () => {
    let frame = 0
    const cbs: FrameRequestCallback[] = []
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb: FrameRequestCallback) => {
      cbs.push(cb)
      return ++frame
    })
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {})
    vi.spyOn(performance, 'now').mockReturnValue(0)

    const { container } = render(<CountUp to={100} suffix="%" dur={1000} />)
    expect(container.textContent).toBe('0%')
    // Advance time past the duration so progress = 1 → final value.
    ;(performance.now as ReturnType<typeof vi.fn>).mockReturnValue(2000)
    act(() => { cbs.forEach(cb => cb(2000)) })
    expect(container.textContent).toBe('100%')

    vi.restoreAllMocks()
  })

  it('formats decimals for non-integer targets', () => {
    vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(1)
    const { container } = render(<CountUp to={1.5} />)
    // initial render value is 0, decimals default to 1 → "0.0"
    expect(container.textContent).toBe('0.0')
    vi.restoreAllMocks()
  })
})

describe('HoloModal', () => {
  it('renders nothing when closed', () => {
    const { container } = render(<HoloModal open={false} onClose={() => {}}>hidden</HoloModal>)
    expect(container).toBeEmptyDOMElement()
    expect(screen.queryByText('hidden')).not.toBeInTheDocument()
  })

  it('renders into a portal and closes on overlay click but not on content click', async () => {
    const onClose = vi.fn()
    render(<HoloModal open onClose={onClose}><span>content</span></HoloModal>)
    expect(screen.getByText('content')).toBeInTheDocument()
    // Clicking the content should not close.
    await userEvent.click(screen.getByText('content'))
    expect(onClose).not.toHaveBeenCalled()
    // Clicking the overlay should close.
    const overlay = document.querySelector('.holo-overlay')!
    await userEvent.click(overlay)
    expect(onClose).toHaveBeenCalledOnce()
  })
})
