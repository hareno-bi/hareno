import { registerMap } from 'echarts/core'
import { useEffect, useState } from 'react'

// Map shapes (public/maps, built by scripts/maps.mjs from Natural Earth): 'world', or a
// country's code (e.g. HKG) for its states or provinces. Loaded once, registered with ECharts.
type Geo = { type: 'FeatureCollection'; features: { properties: { name: string; aliases: string[] } }[] }
export type Country = { code: string; name: string; states: number }

const loaded = new Map<string, Geo>()
const loading = new Map<string, Promise<Geo>>()

export function loadMap(id: string): Promise<Geo> {
  if (!loading.has(id)) {
    loading.set(id, fetch(`/maps/${id}.json`).then((r) => {
      if (!r.ok) throw new Error(`no map ${id}`)
      return r.json()
    }).then((geo: Geo) => {
      registerMap(id, geo as never)
      loaded.set(id, geo)
      return geo
    }))
  }
  return loading.get(id)!
}
export const mapOf = (id: string) => loaded.get(id)

// useMap is whether map id has loaded; it loads it.
export function useMap(id: string | undefined) {
  const [, setReady] = useState(0)
  useEffect(() => {
    if (id && !loaded.has(id)) loadMap(id).then(() => setReady((n) => n + 1), () => {})
  }, [id])
  return !!id && loaded.has(id)
}

// countries lists the maps of states or provinces.
let index: Promise<Country[]> | undefined
export const countries = () => (index ??= fetch('/maps/index.json').then((r) => r.json()).then((x) => x.countries as Country[]))

// placeOf finds the map's name for a dataset's place name: its English name, local or other
// names, or codes (ISO, postal), ignoring case.
const lookups = new Map<string, Map<string, string>>()
export function placeOf(id: string, value: string): string | undefined {
  const geo = loaded.get(id)
  if (!geo) return undefined
  if (!lookups.has(id)) {
    const m = new Map<string, string>()
    for (const { properties: p } of geo.features) for (const a of p.aliases) if (!m.has(a.toLowerCase())) m.set(a.toLowerCase(), p.name)
    for (const { properties: p } of geo.features) m.set(p.name.toLowerCase(), p.name) // a name wins over another's alias
    lookups.set(id, m)
  }
  return lookups.get(id)!.get(value.trim().toLowerCase())
}
