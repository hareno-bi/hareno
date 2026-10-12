// Fails if any npm dependency in the given package-lock.json files (lockfile v3 records each
// package's license) is not in allowed-licenses.txt. Usage: node scripts/check-licenses.mjs <lockfile>...
import { readFileSync } from 'node:fs';

export const allowed = new Set(
  readFileSync(new URL('allowed-licenses.txt', import.meta.url), 'utf8')
    .split('\n').map((l) => l.trim()).filter((l) => l && !l.startsWith('#')),
);

// ponytail: flat SPDX handling (OR = any allowed, AND = all allowed, no nested mixes); use a real
// SPDX parser if a dependency ever ships a nested expression.
export function permitted(expr) {
  if (!expr) return false;
  const ids = expr.replace(/[()]/g, ' ').split(/\s+(?:OR|AND)\s+/).map((s) => s.trim());
  return / AND /.test(expr) ? ids.every((id) => allowed.has(id)) : ids.some((id) => allowed.has(id));
}

export function violations(lock) {
  return Object.entries(lock.packages)
    .filter(([path, pkg]) => path !== '' && !pkg.link && !permitted(pkg.license))
    .map(([path, pkg]) => `${path}: ${pkg.license ?? 'no license field'}`);
}

if (import.meta.main) {
  let bad = 0;
  for (const file of process.argv.slice(2)) {
    const lock = JSON.parse(readFileSync(file, 'utf8'));
    if (lock.lockfileVersion !== 3) throw new Error(`${file}: lockfile v3 required`);
    const v = violations(lock);
    bad += v.length;
    v.forEach((line) => console.error(`${file}: ${line}`));
    if (!v.length) console.log(`${file}: ok (${Object.keys(lock.packages).length - 1} packages)`);
  }
  process.exit(bad ? 1 : 0);
}
