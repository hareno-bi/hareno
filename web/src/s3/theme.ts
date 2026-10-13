import { useEffect, useState } from 'react'

// Colours come from CSS tokens (s3.css), so charts and page share one source of truth.
export type Mode = 'light' | 'dark'
export type Pref = Mode | 'system'

const media = window.matchMedia('(prefers-color-scheme: dark)')

function readPref(): Pref {
  try {
    const v = localStorage.getItem('hareno-theme')
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage blocked: fall back to the system setting
  }
  return 'system'
}

export function useTheme() {
  const [pref, setPref] = useState<Pref>(readPref)
  const [system, setSystem] = useState<Mode>(media.matches ? 'dark' : 'light')
  useEffect(() => {
    const on = () => setSystem(media.matches ? 'dark' : 'light')
    media.addEventListener('change', on)
    return () => media.removeEventListener('change', on)
  }, [])
  const mode: Mode = pref === 'system' ? system : pref
  // Set during render, not in an effect: child effects (chart init) run first and read the tokens.
  document.documentElement.dataset.theme = mode
  useEffect(() => {
    try {
      localStorage.setItem('hareno-theme', pref)
    } catch {
      // not remembered; fine
    }
  }, [mode, pref])
  return { pref, setPref, mode }
}

export const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

// echartsTheme builds the ECharts theme from the current CSS tokens. Call after data-theme is set.
export function echartsTheme() {
  const text = token('--text'), muted = token('--muted'), grid = token('--grid'), surface = token('--surface')
  const axis = {
    axisLine: { lineStyle: { color: grid } },
    axisTick: { show: false },
    axisLabel: { color: muted },
    splitLine: { lineStyle: { color: grid } },
  }
  return {
    color: [1, 2, 3, 4, 5, 6].map((i) => token(`--series-${i}`)),
    backgroundColor: 'transparent',
    textStyle: { fontFamily: token('--font'), color: text },
    legend: { textStyle: { color: muted }, icon: 'roundRect', itemWidth: 12, itemHeight: 8 },
    tooltip: {
      backgroundColor: surface,
      borderColor: grid,
      textStyle: { color: text },
      extraCssText: 'box-shadow: 0 4px 16px rgb(0 0 0 / 0.12); border-radius: 6px;',
    },
    categoryAxis: { ...axis, splitLine: { show: false } },
    valueAxis: { ...axis, axisLine: { show: false } },
  }
}
