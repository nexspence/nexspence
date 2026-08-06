/**
 * saveBlob hands a downloaded response to the browser as a file.
 *
 * These endpoints require an Authorization header, so the file cannot simply be
 * an <a href> the browser fetches on its own — the response has to be requested
 * through the API client and then handed over as an object URL.
 */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/** exportFilename builds the dated name a downloaded export is saved under. */
export function exportFilename(kind: string, as: 'csv' | 'json'): string {
  return `nexspence-${kind}-${new Date().toISOString().slice(0, 10)}.${as}`
}
