import { test } from 'node:test';
import assert from 'node:assert/strict';
import { permitted, violations } from './check-licenses.mjs';

test('SPDX expressions', () => {
  assert.equal(permitted('MIT'), true);
  assert.equal(permitted('(MIT OR GPL-3.0)'), true);
  assert.equal(permitted('(MIT AND GPL-3.0)'), false);
  assert.equal(permitted('GPL-3.0'), false);
  assert.equal(permitted('SEE LICENSE IN LICENSE.txt'), false);
  assert.equal(permitted(undefined), false);
});

test('lockfile violations skip the root package', () => {
  const lock = { packages: { '': {}, 'node_modules/a': { license: 'MIT' }, 'node_modules/b': { license: 'BUSL-1.1' } } };
  assert.deepEqual(violations(lock), ['node_modules/b: BUSL-1.1']);
});
