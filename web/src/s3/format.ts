// Number formats for the sample set. Currency is not agreed yet, so amounts carry no symbol.
const compact = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 })
const whole = new Intl.NumberFormat('en', { maximumFractionDigits: 0 })
const pct = new Intl.NumberFormat('en', { style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 1 })

export const fmtCompact = (v: number) => compact.format(v)
export const fmtWhole = (v: number) => whole.format(v)
export const fmtPct = (v: number) => pct.format(v)
export const fmtDelta = (v: number) => (v >= 0 ? '+' : '−') + pct.format(Math.abs(v))
// Missing values show as an en dash, never as 0 (a null margin is not a zero margin).
export const orDash = (v: number | null | undefined, f: (v: number) => string) => (v == null ? '–' : f(v))
