import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {existsSync, readFileSync} from 'node:fs';
import {lockPackages, productionPackages, serialiseLicenceData} from '../../packages/client-core/src/licences/inventory.ts';
import {licenceSections, parseLicenceData, type LicenceData} from '../../packages/client-core/src/presentation/licences.ts';

const root = new URL('../../', import.meta.url);
const file = new URL('../src/generated/licences.json', import.meta.url);
const data = JSON.parse(readFileSync(file, 'utf8')) as LicenceData;
const REGENERATE = 'run: node --experimental-strip-types web/scripts/licences.ts';

test('the licence list is current: every inventory it was generated from is unchanged', () => {
  assert.ok(Object.keys(data.inputs).includes('package-lock.json'));
  assert.ok(Object.keys(data.inputs).includes('server/go.sum'));
  for (const [rel, digest] of Object.entries(data.inputs)) {
    const now = createHash('sha256').update(readFileSync(new URL(rel, root))).digest('hex');
    assert.equal(now, digest, `${rel} changed since web/src/generated/licences.json was generated; ${REGENERATE}`);
  }
});

test('every production package of the lockfile is listed, and nothing else from npm', () => {
  const lock = JSON.parse(readFileSync(new URL('package-lock.json', root), 'utf8'));
  const expected = productionPackages(lockPackages(lock)).filter(p => !p.name.startsWith('@portico/') && p.name !== 'portico-web').map(p => `${p.name}@${p.version}`).sort();
  const listed = data.components.filter(c => c.id.startsWith('npm:')).map(c => `${c.name}@${c.version}`).sort();
  assert.deepEqual(listed, expected, REGENERATE);
  // Development tooling never appears.
  for (const tool of ['vite', 'typescript', '@vitejs/plugin-react', 'rolldown']) assert.ok(!listed.some(p => p.startsWith(tool + '@')), tool);
});

test('the file is well formed: Portico first, every text present, sources stated', () => {
  assert.ok(parseLicenceData(data));
  assert.equal(data.components[0]!.id, 'portico');
  assert.equal(data.components[0]!.licence, 'GPL-3.0-or-later');
  assert.match(data.texts[data.components[0]!.text!]!, /GNU GENERAL PUBLIC LICENSE\s+Version 3/);
  for (const c of data.components) {
    assert.ok(c.text === undefined || typeof data.texts[c.text] === 'string', c.id);
    assert.ok(c.text !== undefined || c.textPending || (c.textAtUrl && c.url), `${c.id} has no licence text`);
  }
  assert.equal(new Set(data.texts).size, data.texts.length, 'identical texts are stored once');
  assert.deepEqual(data.sources.map(s => s.id), ['portico', 'ffmpeg']);
  assert.deepEqual(licenceSections(data).map(s => s.group), ['portico', 'packages', 'subtitles', 'fonts', 'server']);
  for (const id of ['font:manrope', 'icons:lucide', 'vendor:libass-wasm@4.2.4', 'server:ffmpeg', 'go:std']) assert.ok(data.components.some(c => c.id === id), id);
});

// Where node_modules and Go's module cache are present (a developer's checkout, the release
// build), a fresh run must give exactly the committed file: a dependency change that leaves the
// lockfiles alone (a new Go import of an existing module) is caught too.
const canRegenerate = existsSync(new URL('node_modules/react/package.json', root)) && (() => { try { execFileSync('go', ['version'], {stdio: 'ignore'}); return true; } catch { return false; } })();
test('a fresh generation gives exactly the committed file', {skip: !canRegenerate && 'needs node_modules and Go'}, async () => {
  const {generate} = await import('../scripts/licences.ts');
  assert.equal(serialiseLicenceData(generate()), readFileSync(file, 'utf8'), REGENERATE);
});
