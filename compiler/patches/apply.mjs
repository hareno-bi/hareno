// Patches applied to installed dependencies (run by npm's postinstall). Each patch replaces an
// exact text and fails the install if that text is missing, so a dependency upgrade forces a
// review of the patch instead of silently dropping it.
import { readFileSync, writeFileSync } from 'node:fs';

const patches = [
  {
    // Gate condition 3 (PHASE1-GATE.md, S1 C21): Malloy's fan-out-safe sum on PostgreSQL computes
    // in NUMERIC, then casts the result to DOUBLE PRECISION, losing money precision beyond about
    // 15-16 significant digits. Keep NUMERIC. To be proposed upstream; drop this when released.
    file: 'node_modules/@malloydata/malloy/dist/dialect/postgres/postgres.js',
    from: 'const ret = `CAST(${sumSQL} AS DOUBLE PRECISION)`;',
    to: 'const ret = `CAST(${sumSQL} AS NUMERIC)`; // hareno patch: keep money precision',
  },
];

for (const p of patches) {
  const text = readFileSync(p.file, 'utf8');
  if (text.includes(p.to)) continue; // already applied
  if (!text.includes(p.from)) {
    console.error(`patch target not found in ${p.file}; review compiler/patches/apply.mjs`);
    process.exit(1);
  }
  writeFileSync(p.file, text.replace(p.from, p.to));
  console.log(`patched ${p.file}`);
}
