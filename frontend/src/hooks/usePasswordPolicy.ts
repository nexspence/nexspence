import { useQuery } from '@tanstack/react-query'
import { nexusApi, type AuthConfig } from '@/api/client'

/**
 * usePasswordMinLength mirrors `auth.password_min_length` from
 * /api/v1/auth/config so the password forms can state the rule instead of
 * letting the user discover it through a 400.
 *
 * The service layer remains the authority — this is a mirror, never the
 * check. `undefined` means "no constraint known" (the setting is unwired, the
 * endpoint failed, or the server predates the field), and every form must
 * then submit unhindered and let the server answer.
 */
export function usePasswordMinLength(): number | undefined {
  const { data } = useQuery<AuthConfig>({
    queryKey: ['authConfig'],
    queryFn: () => nexusApi.getAuthConfig(),
    staleTime: 60_000,
  })
  const min = data?.passwordMinLength
  return typeof min === 'number' && min > 0 ? min : undefined
}

/** tooShort reports whether a password violates a known minimum. */
export function tooShort(password: string, min: number | undefined): boolean {
  return min !== undefined && password.length < min
}

/** passwordTooShortMessage is the single wording used by every form. */
export function passwordTooShortMessage(min: number): string {
  return `The password must be at least ${min} characters`
}
