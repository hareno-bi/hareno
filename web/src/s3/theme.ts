import { useEffect, useState } from 'react'
import styleFile from './styles.json'

// A style is a full visual theme: typography, shape, colours for light and dark mode, and chart
// treatment (line, fill, grid, symbols, animation). Hareno is the default; the others were
// designed with Kimi CLI against the same schema (styles.json).
export type Mode = 'light' | 'dark'
export type Pref = Mode | 'system'
type Colours = { bg: string; surface: string; text: string; muted: string; grid: string; accent: string; up: string; down: string
  sub: string; total: string; active: string; heatLo: string; heatHi: string; onSeries: string; series: string[] }
export type ChartStyle = {
  lineWidth: number; smooth: number; symbol: string; symbolSize: number; areaFill: 'none' | 'solid' | 'gradient'; areaOpacity: number
  glow: number; barRadius: number; barMaxWidth: number; barGap: string; gridLineType: 'solid' | 'dashed' | 'dotted' | 'none'
  axisLine: boolean; legendIcon: string; fontSize: number; animationDuration: number; animationEasing: string
  donutRadius: string[]; gaugeWidth: number; heatmapGap: number; tooltipRadius: number
}
export type Style = {
  id: string; name: string; idea: string
  fonts: { body: string; display: string; googleFontsQuery: string }
  shape: { radius: string; borderWidth: string; shadow: string; cardPadding: string; gap: string; headingTransform: string
    headingLetterSpacing: string; kpiWeight: number; numberFeatures: string; backgroundImage: string }
  light: Colours; dark: Colours; chart: ChartStyle
}
export const styles = styleFile.styles as Style[]

const media = window.matchMedia('(prefers-color-scheme: dark)')

function read<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(key) as T | null
    if (v && allowed.includes(v)) return v
  } catch {
    // storage blocked: use the default
  }
  return fallback
}

function save(key: string, v: string) {
  try {
    localStorage.setItem(key, v)
  } catch {
    // not remembered; fine
  }
}

const family = (f: string) => (f === 'system-ui' ? '' : `'${f}', `) + "system-ui, -apple-system, 'Segoe UI', Roboto, Arial, sans-serif"

// apply writes a style's tokens onto <html>, so page CSS and chart options read the same values.
function apply(s: Style, mode: Mode) {
  const c = s[mode], root = document.documentElement.style
  const vars: Record<string, string> = {
    bg: c.bg, surface: c.surface, text: c.text, muted: c.muted, grid: c.grid, accent: c.accent, up: c.up, down: c.down,
    sub: c.sub, total: c.total, active: c.active, 'heat-lo': c.heatLo, 'heat-hi': c.heatHi, 'on-series': c.onSeries,
    font: family(s.fonts.body), 'font-display': family(s.fonts.display),
    radius: s.shape.radius, 'border-width': s.shape.borderWidth, shadow: s.shape.shadow, 'card-padding': s.shape.cardPadding,
    gap: s.shape.gap, 'heading-transform': s.shape.headingTransform, 'heading-spacing': s.shape.headingLetterSpacing,
    'kpi-weight': String(s.shape.kpiWeight), num: s.shape.numberFeatures, 'bg-image': s.shape.backgroundImage,
  }
  c.series.forEach((v, i) => (vars[`series-${i + 1}`] = v))
  for (const [k, v] of Object.entries(vars)) root.setProperty(`--${k}`, v)
  document.documentElement.dataset.theme = mode
}

// loadFonts adds the style's Google Fonts stylesheet once and resolves when its faces are usable,
// since charts draw on canvas and must be re-created after the font arrives.
const loaded = new Set<string>()
async function loadFonts(s: Style) {
  if (!s.fonts.googleFontsQuery || loaded.has(s.id)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = `https://fonts.googleapis.com/css2?${s.fonts.googleFontsQuery}&display=swap`
  document.head.append(link)
  await new Promise((ok) => link.addEventListener('load', ok, { once: true }))
  await Promise.all([s.fonts.body, s.fonts.display].map((f) => document.fonts.load(`16px '${f}'`).catch(() => null)))
  loaded.add(s.id)
}

export function useTheme() {
  const [pref, setPref] = useState<Pref>(() => read('hareno-theme', ['light', 'dark', 'system'], 'system'))
  const [styleId, setStyleId] = useState(() => read('hareno-style', styles.map((s) => s.id), 'hareno'))
  const [system, setSystem] = useState<Mode>(media.matches ? 'dark' : 'light')
  const [fontsFor, setFontsFor] = useState('')
  useEffect(() => {
    const on = () => setSystem(media.matches ? 'dark' : 'light')
    media.addEventListener('change', on)
    return () => media.removeEventListener('change', on)
  }, [])
  const mode: Mode = pref === 'system' ? system : pref
  const style = styles.find((s) => s.id === styleId) ?? styles[0]
  // Applied during render, not in an effect: child effects (chart init) run first and read the tokens.
  apply(style, mode)
  useEffect(() => save('hareno-theme', pref), [pref])
  useEffect(() => {
    save('hareno-style', styleId)
    loadFonts(style).then(() => setFontsFor(style.id))
  }, [style, styleId])
  // Charts wait for fontsReady: ECharts caches text measurements per font name, so drawing with a
  // fallback face first leaves labels sized for the wrong font. key re-creates charts on any change.
  const fontsReady = !style.fonts.googleFontsQuery || fontsFor === style.id
  return { pref, setPref, mode, style, setStyleId, fontsReady, key: `${mode}:${style.id}` }
}

export const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

// echartsTheme builds the ECharts theme from the current tokens and the style's chart treatment.
export function echartsTheme(c: ChartStyle) {
  const text = token('--text'), muted = token('--muted'), grid = token('--grid'), surface = token('--surface')
  const axis = {
    axisLine: { show: c.axisLine, lineStyle: { color: muted } },
    axisTick: { show: false },
    axisLabel: { color: muted, fontSize: c.fontSize },
    nameTextStyle: { color: muted, fontSize: c.fontSize },
    splitLine: { show: c.gridLineType !== 'none', lineStyle: { color: grid, type: c.gridLineType === 'none' ? 'solid' : c.gridLineType } },
  }
  return {
    color: [1, 2, 3, 4, 5, 6].map((i) => token(`--series-${i}`)),
    backgroundColor: 'transparent',
    textStyle: { fontFamily: token('--font'), color: text, fontSize: c.fontSize },
    // 'line' is not an ECharts icon: draw it as a thin bar
    legend: { textStyle: { color: muted, fontSize: c.fontSize }, icon: c.legendIcon === 'line' ? 'rect' : c.legendIcon, itemWidth: 14, itemHeight: c.legendIcon === 'line' ? 3 : 8 },
    tooltip: {
      backgroundColor: surface,
      borderColor: grid,
      textStyle: { color: text, fontFamily: token('--font') },
      extraCssText: `box-shadow: 0 4px 16px rgb(0 0 0 / 0.18); border-radius: ${c.tooltipRadius}px;`,
    },
    categoryAxis: { ...axis, splitLine: { show: false } },
    valueAxis: { ...axis },
    logAxis: { ...axis },
  }
}
