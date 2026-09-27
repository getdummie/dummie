export interface UsageTotals {
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cost_usd: number
  unpriced: boolean
}

export interface UsageReport {
  month: string
  available: boolean
  totals: UsageTotals
  by_model: (UsageTotals & { provider: string, model: string })[]
  by_day: (UsageTotals & { date: string })[]
}

export interface UsageByUser extends UsageTotals {
  user_id: string
  username: string
}

// Months are UTC on the server, so they are here too.
export function monthOptions(count = 12): { value: string, label: string }[] {
  const now = new Date()
  return Array.from({ length: count }, (_, i) => {
    const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - i, 1))
    return {
      value: d.toISOString().slice(0, 7),
      label: d.toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' }),
    }
  })
}

const compact = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 })

export function fmtTokens(n: number): string {
  return n < 10_000 ? n.toLocaleString() : compact.format(n)
}

export function fmtCost(t: UsageTotals): string {
  if (t.cost_usd <= 0) return t.unpriced ? '—' : '$0.00'
  const s = t.cost_usd < 0.01 ? '<$0.01' : `$${t.cost_usd.toFixed(2)}`
  return t.unpriced ? `${s}*` : s
}
