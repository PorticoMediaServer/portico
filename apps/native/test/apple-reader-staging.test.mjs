import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {stageAppleReader} from '../scripts/stage-apple-reader.mjs';

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'apple-reader-stage-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  const input = path.join(root, 'producer'), output = path.join(root, 'app', 'PorticoReader.bundle');
  await fs.mkdir(path.join(input, 'assets'), {recursive: true});
  await fs.writeFile(path.join(input, 'index.html'), '<script type="module" src="./assets/index-a.js"></script>');
  await fs.writeFile(path.join(input, 'assets', 'index-a.js'), 'export const fixture = true;');
  await fs.writeFile(path.join(input, 'assets', 'pdf.worker-a.mjs'), 'export const diagnosticWorker = true;');
  await fs.writeFile(path.join(input, 'licences.json'), '{"fixture":true}');
  return {root, input, output};
}

test('iOS stages the exact final graph and retires stale resources when input is absent', async t => {
  const f = await fixture(t);
  assert.deepEqual(await stageAppleReader(f.input, f.output), {available: true, files: 4});
  assert.equal(await fs.readFile(path.join(f.output, 'assets', 'index-a.js'), 'utf8'), 'export const fixture = true;');
  assert.equal((await fs.stat(path.join(f.output, 'index.html'))).mode & 0o777, 0o644);
  await fs.writeFile(path.join(f.output, 'stale.txt'), 'old');
  await stageAppleReader(f.input, f.output);
  await assert.rejects(fs.stat(path.join(f.output, 'stale.txt')), {code: 'ENOENT'});
  assert.deepEqual(await stageAppleReader(undefined, f.output), {available: false, files: 0});
  await assert.rejects(fs.stat(f.output), {code: 'ENOENT'});
});

for (const [name, change] of [
  ['symlink file', f => fs.symlink(path.join(f.input, 'licences.json'), path.join(f.input, 'assets', 'link.json'))],
  ['symlink directory', f => fs.symlink(f.input, path.join(f.input, 'assets', 'link'))],
  ['hidden source', f => fs.writeFile(path.join(f.input, '.env'), 'not a build asset')],
  ['source tree', f => fs.writeFile(path.join(f.input, 'source.ts'), 'not shipped')],
  ['book fixture', f => fs.writeFile(path.join(f.input, 'private.pdf'), 'not production content')],
  ['missing index', f => fs.rm(path.join(f.input, 'index.html'))],
  ['missing worker', f => fs.rm(path.join(f.input, 'assets', 'pdf.worker-a.mjs'))],
  ['invalid licences', f => fs.writeFile(path.join(f.input, 'licences.json'), 'invalid')],
  ['remote entry', f => fs.writeFile(path.join(f.input, 'index.html'), '<script src="https://server.example/app.js"></script>')],
  ['missing asset', f => fs.writeFile(path.join(f.input, 'index.html'), '<script src="./assets/missing.js"></script>')],
  ['oversized file', async f => { const fd = await fs.open(path.join(f.input, 'assets', 'large.js'), 'w'); try { await fd.truncate(64 * 1024 * 1024 + 1); } finally { await fd.close(); } }],
]) test(`rejects ${name}, preserving the prior staged graph and removing temporary output`, async t => {
  const f = await fixture(t);
  await stageAppleReader(f.input, f.output);
  await change(f);
  await assert.rejects(stageAppleReader(f.input, f.output));
  assert.equal(await fs.readFile(path.join(f.output, 'assets', 'index-a.js'), 'utf8'), 'export const fixture = true;');
  assert.deepEqual(await fs.readdir(path.dirname(f.output)), ['PorticoReader.bundle']);
});

test('input and output never overlap; a symlink root is rejected', async t => {
  const f = await fixture(t);
  await assert.rejects(stageAppleReader(f.input, path.join(f.input, 'PorticoReader.bundle')));
  await assert.rejects(stageAppleReader(f.input, path.join(f.root, 'unrelated')));
  const link = path.join(f.root, 'link'); await fs.symlink(f.input, link);
  await assert.rejects(stageAppleReader(link, f.output));
});

test('only the iOS project stages the bundle before code signing through the native asset origin', async () => {
  const ios = await fs.readFile(new URL('../ios/Portico.xcodeproj/project.pbxproj', import.meta.url), 'utf8');
  const tvos = await fs.readFile(new URL('../tvos/Portico.xcodeproj/project.pbxproj', import.meta.url), 'utf8');
  assert.match(ios, /Stage trusted reader bundle/);
  assert.match(ios, /stage-apple-reader\.mjs/);
  assert.match(ios, /UNLOCALIZED_RESOURCES_FOLDER_PATH/);
  assert.doesNotMatch(tvos, /stage-apple-reader/);
});
