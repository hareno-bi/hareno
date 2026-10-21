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

// tokens are a style's CSS custom properties for one mode. The dashboard element carries the chosen
// style's tokens and chart options read the same map, so cards and charts always match; the app
// chrome (top bar) always uses the Hareno style.
export type Tokens = Record<string, string>
export function tokens(s: Style, mode: Mode): Tokens {
  const c = s[mode]
  const vars: Tokens = {
    bg: c.bg, surface: c.surface, text: c.text, muted: c.muted, grid: c.grid, accent: c.accent, up: c.up, down: c.down,
    sub: c.sub, total: c.total, active: c.active, 'heat-lo': c.heatLo, 'heat-hi': c.heatHi, 'on-series': c.onSeries,
    font: family(s.fonts.body), 'font-display': family(s.fonts.display),
    radius: s.shape.radius, 'border-width': s.shape.borderWidth, shadow: s.shape.shadow, 'card-padding': s.shape.cardPadding,
    gap: s.shape.gap, 'heading-transform': s.shape.headingTransform, 'heading-spacing': s.shape.headingLetterSpacing,
    'kpi-weight': String(s.shape.kpiWeight), num: s.shape.numberFeatures, 'bg-image': s.shape.backgroundImage,
  }
  c.series.forEach((v, i) => (vars[`series-${i + 1}`] = v))
  return Object.fromEntries(Object.entries(vars).map(([k, v]) => [`--${k}`, v]))
}

function applyChrome(mode: Mode) {
  for (const [k, v] of Object.entries(tokens(styles[0], mode))) document.documentElement.style.setProperty(k, v)
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

// fixed: a light or dark mode and a style that override the viewer's saved choice without changing
// it (an embedded dashboard takes them from its address)
export function useTheme(fixed: { pref?: Pref; style?: string } = {}) {
  const [pref, setPref] = useState<Pref>(() => read('hareno-theme', ['light', 'dark', 'system'], 'system'))
  const [styleId, setStyleId] = useState(() => read('hareno-style', styles.map((s) => s.id), 'hareno'))
  const [system, setSystem] = useState<Mode>(media.matches ? 'dark' : 'light')
  const [fontsFor, setFontsFor] = useState('')
  useEffect(() => {
    const on = () => setSystem(media.matches ? 'dark' : 'light')
    media.addEventListener('change', on)
    return () => media.removeEventListener('change', on)
  }, [])
  const shown = fixed.pref ?? pref
  const mode: Mode = shown === 'system' ? system : shown
  const style = styles.find((s) => s.id === (fixed.style ?? styleId)) ?? styles[0]
  applyChrome(mode)
  useEffect(() => save('hareno-theme', pref), [pref])
  useEffect(() => {
    save('hareno-style', styleId)
    loadFonts(style).then(() => setFontsFor(style.id))
  }, [style, styleId])
  // Charts wait for fontsReady: ECharts caches text measurements per font name, so drawing with a
  // fallback face first leaves labels sized for the wrong font. key re-creates charts on any change.
  const fontsReady = !style.fonts.googleFontsQuery || fontsFor === style.id
  return { pref, setPref, mode, style, setStyleId, fontsReady, vars: tokens(style, mode), key: `${mode}:${style.id}` }
}

// echartsTheme builds the ECharts theme from the dashboard's tokens and the style's chart treatment.
export function echartsTheme(c: ChartStyle, t: Tokens) {
  const token = (name: string) => t[name]
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

// treatment is a style's chart look as ECharts series settings: line and bar series, colours,
// glow, the plot grid and the animation. The SQL editor's charts use it too.
export function treatment(c: ChartStyle, t: Tokens) {
  const token = (name: string) => t[name]
  const colour = (i: number) => token(`--series-${(i % 6) + 1}`)
  const glow = (i: number) => (c.glow ? { shadowBlur: c.glow, shadowColor: colour(i) } : {})
  const area = (i: number) =>
    c.areaFill === 'none' ? undefined
      : c.areaFill === 'solid' ? { opacity: c.areaOpacity }
      : { opacity: 1, color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [
          { offset: 0, color: colour(i) + Math.round(c.areaOpacity * 255).toString(16).padStart(2, '0') },
          { offset: 1, color: colour(i) + '00' }] } }
  const line = (i: number) => ({
    type: 'line', smooth: c.smooth, symbol: c.symbol, symbolSize: c.symbolSize, showSymbol: c.symbol !== 'none',
    lineStyle: { width: c.lineWidth, ...glow(i) }, areaStyle: area(i),
  })
  const bar = (i: number) => ({
    type: 'bar', barMaxWidth: c.barMaxWidth, barGap: c.barGap, itemStyle: { ...glow(i) },
  })
  const anim = { animationDuration: c.animationDuration, animationEasing: c.animationEasing as 'cubicOut' } // any ECharts easing name
  const grid = { left: 8, right: 16, top: 40, bottom: 8, containLabel: true }
  return { token, colour, glow, area, line, bar, anim, grid, legend: { top: 0, left: 0 } }
}
