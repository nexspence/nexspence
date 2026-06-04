import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen, within, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import DocsPage from './DocsPage'
import { renderWithProviders } from '@/test/renderUtils'

const ORIGIN = 'http://localhost'

function clipboardMock() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })
  return writeText
}

describe('DocsPage', () => {
  beforeEach(() => {
    clipboardMock()
    // The stubbed window.location has no `origin`; DocsPage reads it as `base`.
    Object.assign(window.location, { origin: ORIGIN })
  })

  it('renders the Getting Started section by default', () => {
    renderWithProviders(<DocsPage />)
    expect(screen.getByRole('heading', { name: 'Getting Started' })).toBeInTheDocument()
    expect(screen.getByText('Your Base URL')).toBeInTheDocument()
    expect(screen.getByText('Authentication')).toBeInTheDocument()
  })

  it('renders all guide nav buttons and format nav buttons', () => {
    renderWithProviders(<DocsPage />)
    expect(screen.getByRole('button', { name: /Creating Repositories/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Managing Users/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Roles & Privileges/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Content Selectors/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Security Scanning/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cleanup Policies/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /API Tokens/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Maven 2/3' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Docker / OCI' })).toBeInTheDocument()
  })

  it('switches through every guide section', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DocsPage />)

    await user.click(screen.getByRole('button', { name: /Creating Repositories/ }))
    expect(screen.getByRole('heading', { name: 'Creating Repositories' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Managing Users/ }))
    expect(screen.getByRole('heading', { name: 'Managing Users' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Roles & Privileges/ }))
    expect(screen.getByRole('heading', { name: 'Roles & Privileges' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Content Selectors/ }))
    expect(screen.getByRole('heading', { name: 'Content Selectors' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Security Scanning/ }))
    expect(screen.getByRole('heading', { name: 'Security Scanning' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Cleanup Policies/ }))
    expect(screen.getByRole('heading', { name: 'Cleanup Policies' })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /API Tokens/ }))
    expect(screen.getByRole('heading', { name: 'API Tokens' })).toBeInTheDocument()
  })

  it('switches through every format section', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DocsPage />)
    // Some formats have a leading emoji in the button label (no iconUrl => emoji span).
    const formats: { label: RegExp; heading: RegExp }[] = [
      { label: /Maven 2\/3/, heading: /Maven 2\/3/ },
      { label: /^npm$/, heading: /npm/ },
      { label: /PyPI/, heading: /PyPI/ },
      { label: /Docker \/ OCI/, heading: /Docker \/ OCI/ },
      { label: /Go Modules/, heading: /Go Modules/ },
      { label: /NuGet/, heading: /NuGet/ },
      { label: /Raw/, heading: /Raw/ },
      { label: /Helm/, heading: /Helm/ },
      { label: /Cargo \(Rust\)/, heading: /Cargo \(Rust\)/ },
      { label: /Apt \/ Debian/, heading: /Apt \/ Debian/ },
      { label: /Yum \/ RPM/, heading: /Yum \/ RPM/ },
      { label: /Conan C\/C\+\+/, heading: /Conan C\/C\+\+/ },
      { label: /Conda/, heading: /Conda/ },
      { label: /Terraform/, heading: /Terraform/ },
    ]
    for (const f of formats) {
      await user.click(screen.getByRole('button', { name: f.label }))
      expect(screen.getByRole('heading', { name: f.heading })).toBeInTheDocument()
    }
  })

  it('copies a CodeBlock to the clipboard and shows "Copied!"', async () => {
    const writeText = clipboardMock()
    const { container } = renderWithProviders(<DocsPage />)
    // CodeBlock copy buttons live inside .codeHeader and show "Copied!" (with bang)
    // after a successful copy. UrlBlock buttons (only "Copied") are excluded.
    const codeCopyBtn = container.querySelector<HTMLButtonElement>(
      '[class*="codeHeader"] button'
    )
    expect(codeCopyBtn).toBeTruthy()
    // Use fireEvent so user-event's own clipboard shim does not interfere.
    fireEvent.click(codeCopyBtn as HTMLButtonElement)
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    expect(await screen.findByText('Copied!')).toBeInTheDocument()
  })

  it('copies the base URL via the UrlBlock copy button', async () => {
    const writeText = clipboardMock()
    renderWithProviders(<DocsPage />)
    const urlValue = screen.getByText(ORIGIN)
    const urlBlock = urlValue.parentElement as HTMLElement
    const copyBtn = within(urlBlock).getByRole('button')
    fireEvent.click(copyBtn)
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(ORIGIN))
    expect(await within(urlBlock).findByText('Copied')).toBeInTheDocument()
  })

  it('renders a format note box (apt) and code labels', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DocsPage />)
    await user.click(screen.getByRole('button', { name: 'Apt / Debian' }))
    expect(screen.getByText(/Replace "focal main" with your distribution/)).toBeInTheDocument()
    // Labeled code blocks appear in the apt install section.
    expect(screen.getByText('Using apt-get:')).toBeInTheDocument()
  })

  it('falls back to placeholder when a screenshot image fails to load', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DocsPage />)
    await user.click(screen.getByRole('button', { name: /Creating Repositories/ }))
    const imgs = screen.getAllByRole('img')
    const screenshot = imgs.find(i => (i as HTMLImageElement).src.includes('/docs/screenshots/'))
    expect(screenshot).toBeTruthy()
    fireEvent.error(screenshot as HTMLImageElement)
    expect(await screen.findByText('📸 Screenshot')).toBeInTheDocument()
  })

  it('hides a format nav brand icon when it fails to load', async () => {
    const { container } = renderWithProviders(<DocsPage />)
    // Nav brand icons have alt="" (presentational), so query the DOM directly.
    const navIcons = Array.from(
      container.querySelectorAll<HTMLImageElement>('img')
    ).filter(i => i.src.includes('cdn.simpleicons.org'))
    expect(navIcons.length).toBeGreaterThan(0)
    const icon = navIcons[0]
    fireEvent.error(icon)
    await waitFor(() => expect(icon.style.display).toBe('none'))
  })
})
