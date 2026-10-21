// maps.mjs builds the map shapes in public/maps from Natural Earth (public domain):
// world.json (countries, 1:50m) and one file per country of its states or provinces (1:10m),
// each feature with its English name and the other names a dataset may use for it (aliases),
// plus index.json listing the countries. Run it to update the shapes: node scripts/maps.mjs
import { mkdir, writeFile } from 'node:fs/promises'

const release = 'v5.1.2'
const src = (f) => `https://raw.githubusercontent.com/nvkelso/natural-earth-vector/${release}/geojson/${f}.geojson`
const out = new URL('../public/maps/', import.meta.url)

// Names Natural Earth lacks or gets wrong, by its code (ADM0_A3 for countries, iso_3166_2 for states)
const extra = {
  TWN: ['台灣', '臺灣'],
  'HK-X01~': ['中西區'], 'HK-X02~': ['灣仔區'], 'HK-X03~': ['東區'], 'HK-X04~': ['南區'], 'HK-X05~': ['油尖旺區'],
  'HK-X06~': ['九龍城區'], 'HK-X07~': ['深水埗區'], 'HK-X08~': ['黃大仙區'], 'HK-X09~': ['觀塘區'], 'HK-X10~': ['西貢區'],
  'HK-X11~': ['沙田區'], 'HK-X12~': ['葵青區'], 'HK-X13~': ['荃灣區'], 'HK-X14~': ['屯門區'], 'HK-X15~': ['元朗區'],
  'HK-X16~': ['北區'], 'HK-X17~': ['大埔區'], 'HK-X18~': ['離島區'],
}
// Natural Earth's own Chinese names that are wrong (e.g. 北方 for North district): dropped
const wrong = new Set(['北方', '南方', '東方', '中西部', '北方区', '南方区'])

// Coordinates to 3 decimals (about 100 m), repeated points dropped: a third of the size.
const round = (c) => {
  if (typeof c[0] === 'number') return [Math.round(c[0] * 1e3) / 1e3, Math.round(c[1] * 1e3) / 1e3]
  const r = c.map(round)
  return typeof r[0]?.[0] === 'number' ? r.filter((p, i) => i === 0 || p[0] !== r[i - 1][0] || p[1] !== r[i - 1][1]) : r
}
const names = (p, keys, code) => [...new Set([...keys.flatMap((k) => String(p[k] ?? '').split('|')), ...(extra[code] ?? [])]
  .map((s) => s.trim()).filter((s) => s && s !== '-99' && !wrong.has(s)))]
const feature = (name, aliases, g) => ({ type: 'Feature', properties: { name, aliases }, geometry: { type: g.type, coordinates: round(g.coordinates) } })
const get = async (f) => (await (await fetch(src(f))).json()).features

await mkdir(out, { recursive: true })
const countries = await get('ne_50m_admin_0_countries')
const world = countries.map((c) => {
  const p = c.properties
  return feature(p.NAME, names(p, ['NAME_LONG', 'ADMIN', 'NAME_ALT', 'FORMAL_EN', 'ABBREV', 'ISO_A2_EH', 'ISO_A3_EH', 'ADM0_A3', 'NAME_JA', 'NAME_ZH', 'NAME_ZHT'], p.ADM0_A3), c.geometry)
})
await writeFile(new URL('world.json', out), JSON.stringify({ type: 'FeatureCollection', features: world }))

const states = Map.groupBy(await get('ne_10m_admin_1_states_provinces'), (s) => s.properties.adm0_a3)
const index = []
for (const [code, fs] of states) {
  if (fs.length < 2) continue // a country of one area has no states to map
  const p0 = fs[0].properties
  index.push({ code, name: p0.admin, states: fs.length })
  const features = fs.map((s) => {
    const p = s.properties
    return feature(p.name, names(p, ['name_en', 'name_alt', 'name_local', 'iso_3166_2', 'postal', 'code_hasc', 'name_ja', 'name_zh', 'name_zht'], p.iso_3166_2), s.geometry)
  })
  await writeFile(new URL(`${code}.json`, out), JSON.stringify({ type: 'FeatureCollection', features }))
}
index.sort((a, b) => a.name.localeCompare(b.name))
await writeFile(new URL('index.json', out), JSON.stringify({ release, countries: index }))
console.log(`world: ${world.length} countries; ${index.length} countries' states`)
