import type { LocationQueryValue } from 'vue-router'

// safeRedirect narrows a ?redirect= query value to a same-origin path. The only
// producer of it today is the API's /login hand-off, but it arrives as a URL in
// the address bar, so anyone can put anything there: "//evil.example" and
// "https://evil.example" are both other origins to a browser, and sending a
// just-signed-in user to one is how a sign-in page becomes an open redirect.
export function safeRedirect(value: LocationQueryValue | LocationQueryValue[] | undefined): string | null {
  const raw = Array.isArray(value) ? value[0] : value
  if (typeof raw !== 'string' || !raw.startsWith('/') || raw.startsWith('//')) return null
  return raw
}
