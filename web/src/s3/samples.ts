import type { Kind, Labels, Result, Spec, Value } from '../sql/chart'

// Every chart type, as a dashboard tile draws it, on the sample cargo data (charts.json): each
// sample is a query result worked out from that data and the tile's chart settings.
type Data = {
  months: string[]
  byFlow: { name: string; data: number[] }[]
  byCargo: { name: string; data: number[] }[]
  monthly: { tonnes: number; onTime: number }[]
  airlines: { name: string; tonnes: number }[]
  shipments: [number, number, string][]
  trucks: [number, number, number][]
  occupancy: number
}
export type Sample = { title: string; sub: string; wide?: boolean; result: Result; spec: Spec }

const display: Labels = {
  month: { label: 'Month' }, flow: { label: 'Flow' }, cargo: { label: 'Cargo type' }, airline: { label: 'Airline' },
  tonnes: { label: 'Tonnes', format: 'integer' }, prior: { label: 'Prior 12 months', format: 'integer' }, on_time: { label: 'Ready by cut-off', format: 'percent' },
  occupancy: { label: 'Occupancy', format: 'percent' }, shipment: { label: 'Shipment' }, weight: { label: 'Weight (kg)', format: 'integer' },
  hours: { label: 'Hours in terminal', format: 'number' }, shipments: { label: 'Shipments', format: 'integer' }, dwell: { label: 'Time in terminal' },
  day: { label: 'Day' }, part: { label: 'Part of day' }, trucks: { label: 'Trucks', format: 'integer' }, quarter: { label: 'Quarter' },
  first: { label: 'First month', format: 'integer' }, last: { label: 'Last month', format: 'integer' }, low: { label: 'Lowest month', format: 'integer' },
  high: { label: 'Highest month', format: 'integer' }, avg_weight: { label: 'Average weight (kg)', format: 'integer' }, avg_hours: { label: 'Average hours', format: 'number' },
  country: { label: 'Country' }, district: { label: 'District' }, airport: { label: 'Airport' }, lon: { label: 'Longitude' }, lat: { label: 'Latitude' },
  change: { label: 'Change on prior 12 months', format: 'integer' },
}
const res = (columns: string[], rows: Value[][]): Result => ({ columns, rows, truncated: false, display })
const spec = (kind: Kind, x: string, ys: string[], split = '', more: Partial<Spec> = {}): Spec => ({ kind, x, ys, split, ...more })
const sum = (a: number[]) => a.reduce((x, y) => x + y, 0)
const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const band = (h: number) => (h < 12 ? 'Under 12 h' : h < 24 ? '12 to 24 h' : 'Over 24 h')

export function samples(d: Data): Sample[] {
  const n = d.months.length, last12 = d.months.slice(-12)
  const recent = (data: number[]) => data.slice(-12)
  const flows = res(['month', 'flow', 'tonnes'], d.byFlow.flatMap((f) => recent(f.data).map((v, i) => [last12[i], f.name, v])))
  const cargo = res(['month', 'cargo', 'tonnes'], d.byCargo.flatMap((c) => recent(c.data).map((v, i) => [last12[i], c.name, v])))
  const monthly = res(['month', 'tonnes', 'on_time'], d.monthly.map((m, i) => [d.months[i], m.tonnes, m.onTime]))
  const cargoYear = res(['cargo', 'tonnes'], d.byCargo.map((c) => [c.name, sum(recent(c.data))]))
  const airlines = res(['airline', 'tonnes'], d.airlines.map((a) => [a.name, a.tonnes]))
  const shipments = res(['shipment', 'flow', 'weight', 'hours'], d.shipments.map(([w, h, f], i) => [i + 1, f, w, h]))
  const dwell = new Map<string, number>()
  d.shipments.forEach(([, h, f]) => dwell.set(`${f}|${band(h)}`, (dwell.get(`${f}|${band(h)}`) ?? 0) + 1))
  const flowDwell = res(['flow', 'dwell', 'shipments'], [...dwell].map(([k, v]) => [...k.split('|'), v]))
  const part = (h: number) => (h < 8 ? 'Night' : h < 16 ? 'Day' : 'Evening')
  const trucks = res(['day', 'part', 'trucks'], d.trucks.map(([h, day, t]) => [days[day], part(h), t]))
  const hourly = res(['hour', 'day', 'trucks'], d.trucks.map(([h, day, t]) => [String(h).padStart(2, '0'), days[day], t]))
  // a day's trucks: its weekday's average day, scaled by its month's tonnage against the average month
  const perDay = days.map((_, day) => sum(d.trucks.filter((t) => t[1] === day).map((t) => t[2])))
  const meanTonnes = sum(d.monthly.map((m) => m.tonnes)) / n
  const calendarRows: Value[][] = []
  for (const [i, m] of d.months.slice(-6).entries()) {
    const [y, mo] = m.split('-').map(Number), scale = d.monthly[n - 6 + i].tonnes / meanTonnes
    for (let day = 1; day <= new Date(y, mo, 0).getDate(); day++) {
      const wd = (new Date(y, mo - 1, day).getDay() + 6) % 7
      calendarRows.push([`${m}-${String(day).padStart(2, '0')}`, Math.round(perDay[wd] * scale)])
    }
  }
  const quarters: Value[][] = []
  for (let i = 0; i + 3 <= n; i += 3) {
    const t = d.monthly.slice(i, i + 3).map((m) => m.tonnes)
    quarters.push([`${d.months[i]} to ${d.months[i + 2]}`, t[0], t[2], Math.min(...t), Math.max(...t)])
  }
  const flowYears = d.byFlow.map((f) => [f.name, sum(f.data.slice(-12)), sum(f.data.slice(-24, -12))] as const)
  const byFlow = new Map<string, number[][]>()
  d.shipments.forEach(([w, h, f]) => byFlow.set(f, [...(byFlow.get(f) ?? []), [w, h]]))
  const steps = [6, 12, 24, 48].map((t) => [`Still in terminal after ${t} h`, d.shipments.filter((s) => s[1] >= t).length])
  // sample places: the cargo data has none
  const countries = res(['country', 'tonnes'], [['Hong Kong', 120], ['Japan', 300], ['Taiwan', 180], ['United States', 420], ['Germany', 210],
    ['United Kingdom', 160], ['Australia', 90], ['Brazil', 70], ['Singapore', 110], ['India', 150]])
  const districts = res(['district', 'trucks'], [['Central and Western', 95], ['Wan Chai', 70], ['Eastern', 40], ['Yau Tsim Mong', 85], ['Kowloon City', 55],
    ['Kwun Tong', 60], ['Sha Tin', 50], ['Tsuen Wan', 35], ['Kwai Tsing', 120], ['Yuen Long', 20], ['Tuen Mun', 15], ['Islands', 140]])
  const airports = res(['airport', 'lon', 'lat', 'tonnes'], [['Hong Kong', 113.92, 22.31, 500], ['Tokyo Narita', 140.39, 35.77, 220], ['Taipei Taoyuan', 121.23, 25.08, 180],
    ['Singapore', 103.99, 1.36, 200], ['London Heathrow', -0.45, 51.47, 160], ['Frankfurt', 8.57, 50.03, 190], ['Anchorage', -149.99, 61.17, 260], ['Los Angeles', -118.41, 33.94, 210]])

  return [
    { title: 'Tonnage, latest month', sub: 'KPI card', result: monthly, spec: spec('kpi', 'month', ['tonnes']) },
    { title: 'Warehouse occupancy', sub: 'Gauge', result: res(['occupancy'], [[d.occupancy]]), spec: spec('gauge', '', ['occupancy']) },
    { title: 'Tonnage by cargo type', sub: 'Column', result: cargoYear, spec: spec('bar', 'cargo', ['tonnes']) },
    { title: 'Top 10 airlines', sub: 'Bar', result: airlines, spec: spec('hbar', 'airline', ['tonnes']) },
    { title: 'Tonnage by month and flow', sub: 'Stacked column', wide: true, result: flows, spec: spec('stacked', 'month', ['tonnes'], 'flow') },
    { title: 'Trucks by day and part of day', sub: 'Stacked bar', result: trucks, spec: spec('hstacked', 'day', ['trucks'], 'part') },
    { title: 'Trucks by day: share by part of day', sub: '100% stacked bar', result: trucks, spec: spec('hpct', 'day', ['trucks'], 'part') },
    { title: 'Cargo mix by month', sub: '100% stacked column', wide: true, result: cargo, spec: spec('pct', 'month', ['tonnes'], 'cargo') },
    { title: 'Tonnage by month and flow', sub: 'Line', wide: true, result: flows, spec: spec('line', 'month', ['tonnes'], 'flow') },
    { title: 'Tonnage by month', sub: 'Area', result: monthly, spec: spec('area', 'month', ['tonnes']) },
    { title: 'Tonnage by month and cargo type', sub: 'Stacked area', result: cargo, spec: spec('stackedArea', 'month', ['tonnes'], 'cargo') },
    { title: 'Tonnage and build-up punctuality', sub: 'Column and line, two axes', wide: true, result: monthly, spec: spec('combo', 'month', ['tonnes', 'on_time']) },
    { title: 'Tonnage by flow against the prior 12 months', sub: 'Bullet', result: res(['flow', 'tonnes', 'prior'], flowYears.map((f) => [...f])), spec: spec('bullet', 'flow', ['tonnes', 'prior']) },
    { title: 'Growth by flow on the prior 12 months', sub: 'Waterfall', result: res(['flow', 'change'], flowYears.map(([f, a, b]) => [f, a - b])), spec: spec('waterfall', 'flow', ['change']) },
    { title: 'Monthly tonnage by quarter', sub: 'Candlestick: first, last, lowest and highest month', wide: true,
      result: res(['quarter', 'first', 'last', 'low', 'high'], quarters), spec: spec('candlestick', 'quarter', ['first', 'last', 'low', 'high']) },
    { title: 'Truck arrivals by day', sub: 'Calendar heat map, last 6 months', wide: true, result: res(['day', 'trucks'], calendarRows), spec: spec('calendar', 'day', ['trucks']) },
    { title: 'Cargo mix, last 12 months', sub: 'Pie', result: cargoYear, spec: spec('pie', 'cargo', ['tonnes']) },
    { title: 'Shipments still in the terminal', sub: 'Funnel', result: res(['dwell', 'shipments'], steps), spec: spec('funnel', 'dwell', ['shipments']) },
    { title: 'Top 10 airlines', sub: 'Treemap', result: airlines, spec: spec('treemap', 'airline', ['tonnes']) },
    { title: 'Shipments by flow and time in terminal', sub: 'Sunburst', result: flowDwell, spec: spec('sunburst', 'dwell', ['shipments'], 'flow') },
    { title: 'Truck arrivals by hour and weekday', sub: 'Heat map', wide: true, result: hourly, spec: spec('heatmap', 'hour', ['trucks'], 'day') },
    { title: 'Shipments from flow to time in terminal', sub: 'Sankey', result: flowDwell, spec: spec('sankey', 'flow', ['shipments'], 'dwell') },
    { title: 'Shipments between flow and time in terminal', sub: 'Chord', result: flowDwell, spec: spec('chord', 'flow', ['shipments'], 'dwell') },
    { title: 'Trucks by weekday and part of day', sub: 'Radar', result: trucks, spec: spec('radar', 'day', ['trucks'], 'part') },
    { title: 'Shipment weight against dwell time', sub: 'Scatter', result: shipments, spec: spec('scatter', 'weight', ['hours'], 'flow') },
    { title: 'Flows: weight, dwell and shipments', sub: 'Bubble', result: res(['flow', 'avg_weight', 'avg_hours', 'shipments'],
      [...byFlow].map(([f, s]) => [f, sum(s.map((x) => x[0])) / s.length, sum(s.map((x) => x[1])) / s.length, s.length])), spec: spec('bubble', 'avg_weight', ['avg_hours', 'shipments']) },
    { title: 'Hours in terminal', sub: 'Histogram', result: shipments, spec: spec('histogram', 'shipment', ['hours']) },
    { title: 'Hours in terminal by flow', sub: 'Box plot', result: shipments, spec: spec('boxplot', 'flow', ['hours']) },
    { title: 'Hours in terminal by flow', sub: 'Beeswarm', wide: true, result: shipments, spec: spec('beeswarm', 'flow', ['hours']) },
    { title: 'Export tonnes by destination (sample places)', sub: 'Filled map, world', wide: true, result: countries, spec: spec('map', 'country', ['tonnes']) },
    { title: 'Truck pick-ups by district (sample places)', sub: 'Filled map, Hong Kong districts', result: districts, spec: spec('map', 'district', ['trucks'], '', { map: 'HKG' }) },
    { title: 'Tonnes by airport (sample places)', sub: 'Point map', result: airports, spec: spec('pointmap', 'airport', ['lon', 'lat', 'tonnes']) },
  ]
}
