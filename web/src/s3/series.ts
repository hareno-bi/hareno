import { registerCustomSeries } from 'echarts/core'
import type { CustomSeriesRenderItem } from 'echarts/types/dist/shared'

// Hareno's own ECharts series, for charts ECharts does not draw: registered by name (ECharts 6),
// used as { type: 'custom', renderItem: 'hareno.waffle', ... }, and themed, animated and clicked
// like ECharts' own. Each draws one data item; api.style() is the series' theme colour.

// waffle: a 10 x 10 grid, one square per percent; an item's value is its square's number (0-99).
// itemPayload: { top, bottom, radius } (room for the legend; corner rounding)
const waffle: CustomSeriesRenderItem = (params, api) => {
  const p = params.itemPayload as { top: number; bottom: number; radius: number }
  const w = api.getWidth(), h = api.getHeight() - p.top - p.bottom
  const side = Math.min(w, h) / 10, gap = Math.max(1, side * 0.08)
  const cell = Number(api.value(0)), x0 = (w - side * 10) / 2, y0 = p.top + (h - side * 10) / 2
  return {
    type: 'rect', shape: { x: x0 + (cell % 10) * side, y: y0 + Math.floor(cell / 10) * side, width: side - gap, height: side - gap, r: p.radius },
    style: api.style(),
  }
}

// marimekko: on value axes 0-1, an item is [x0, x1, y0, y1, column]: a column's width is its share
// of the total, a segment's height its share of the column. A column's bottom segment labels it.
// itemPayload: { columns: names, color: label colour }
const marimekko: CustomSeriesRenderItem = (params, api) => {
  const p = params.itemPayload as { columns: string[]; color: string }
  const [x0, x1, y0, y1] = [0, 1, 2, 3].map((i) => Number(api.value(i)))
  const a = api.coord([x0, y1]), b = api.coord([x1, y0])
  const rect = { type: 'rect', shape: { x: a[0] + 1, y: a[1], width: Math.max(b[0] - a[0] - 2, 1), height: b[1] - a[1] }, style: api.style() } as const
  if (y0 > 0) return rect
  return { type: 'group', children: [rect, { type: 'text', x: (a[0] + b[0]) / 2, y: b[1] + 6,
    style: { text: p.columns[Number(api.value(4))], fill: p.color, align: 'center', verticalAlign: 'top', fontSize: 11, width: b[0] - a[0], overflow: 'truncate' } }] }
}

// ribbon: on a category X axis, an item is [column, y0, y1, nextY0, nextY1]: a stacked segment, and
// the ribbon to the same series' segment in the next column (absent in the last).
const ribbon: CustomSeriesRenderItem = (_params, api) => {
  const i = Number(api.value(0)), [y0, y1, n0, n1] = [1, 2, 3, 4].map((k) => api.value(k))
  const band = (api.size!([1, 0]) as number[])[0], half = band * 0.25
  const top = api.coord([i, y1]), bottom = api.coord([i, y0])
  const children: object[] = [{ type: 'rect', shape: { x: top[0] - half, y: top[1], width: half * 2, height: bottom[1] - top[1] }, style: api.style() }]
  if (n0 !== null && n0 !== undefined && n0 !== '' && !isNaN(Number(n0))) {
    const nt = api.coord([i + 1, n1]), nb = api.coord([i + 1, n0])
    children.push({ type: 'polygon', shape: { points: [[top[0] + half, top[1]], [nt[0] - half, nt[1]], [nb[0] - half, nb[1]], [bottom[0] + half, bottom[1]]] },
      style: api.style({ opacity: 0.35 }) })
  }
  return { type: 'group', children } as ReturnType<CustomSeriesRenderItem>
}

// gantt: on a time X axis and a category Y axis, an item is [start, end, row]: a bar from start to end.
// itemPayload: { radius }
const gantt: CustomSeriesRenderItem = (params, api) => {
  const p = params.itemPayload as { radius: number }
  const row = api.value(2), start = api.coord([api.value(0), row]), end = api.coord([api.value(1), row])
  const h = (api.size!([0, 1]) as number[])[1] * 0.6
  return { type: 'rect', shape: { x: start[0], y: start[1] - h / 2, width: Math.max(end[0] - start[0], 2), height: h, r: p.radius }, style: api.style() }
}

// packed: circles laid out beforehand (pack), an item is [x, y, radius] in the layout's units,
// fitted to the chart. itemPayload: { extent: [x0, y0, x1, y1], top, color: label colour, names }
const packed: CustomSeriesRenderItem = (params, api) => {
  const p = params.itemPayload as { extent: number[]; top: number; color: string; names: string[] }
  const [ex0, ey0, ex1, ey1] = p.extent, w = api.getWidth(), h = api.getHeight() - p.top
  const k = Math.min(w / (ex1 - ex0), h / (ey1 - ey0))
  const cx = (w - (ex1 - ex0) * k) / 2 + (Number(api.value(0)) - ex0) * k, cy = p.top + (h - (ey1 - ey0) * k) / 2 + (Number(api.value(1)) - ey0) * k
  const r = Number(api.value(2)) * k
  const name = p.names[params.dataIndex] ?? ''
  return { type: 'group', children: [
    { type: 'circle', shape: { cx, cy, r: Math.max(r - 1, 0.5) }, style: api.style() },
    { type: 'text', x: cx, y: cy, silent: true, style: { text: r > 18 ? name : '', fill: p.color, align: 'center', verticalAlign: 'middle', fontSize: 11, width: r * 1.8, overflow: 'truncate' } },
  ] }
}

registerCustomSeries('hareno.waffle', waffle)
registerCustomSeries('hareno.marimekko', marimekko)
registerCustomSeries('hareno.ribbon', ribbon)
registerCustomSeries('hareno.gantt', gantt)
registerCustomSeries('hareno.packed', packed)

// pack lays circles of the given radii out close together: largest first, each at the spot nearest
// the centre that touches a circle already placed and overlaps none.
// ponytail: 24 spots per placed circle, O(n^3): 100 circles take about 0.2 s, so charts show at most 100; a front-chain packer if more
export function pack(radii: number[]): [number, number][] {
  const order = radii.map((r, i) => [r, i] as const).sort((a, b) => b[0] - a[0])
  const placed: [number, number, number][] = [], at: [number, number][] = Array(radii.length)
  for (const [r, i] of order) {
    let best: [number, number] = [0, 0], dist = placed.length ? Infinity : 0
    for (const [px, py, pr] of placed) for (let a = 0; a < 24; a++) {
      const x = px + (pr + r) * Math.cos((a * Math.PI) / 12), y = py + (pr + r) * Math.sin((a * Math.PI) / 12), d = Math.hypot(x, y)
      if (d < dist && placed.every(([qx, qy, qr]) => Math.hypot(qx - x, qy - y) >= qr + r - 1e-9)) [best, dist] = [[x, y], d]
    }
    placed.push([...best, r]); at[i] = best
  }
  return at
}
