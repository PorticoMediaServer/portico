import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {kitFamilies, readGalleryInputs} from '../scripts/config.mjs';

const layout = ['referenceHtml', 'searchHtml', 'tvHtml', 'phoneHtml', 'browseJson', 'browseHtml', 'readHtml', 'phoneReadHtml'];
const browse = ['movieCapabilities', 'musicCapabilities', 'songResponse', 'drawnListen', 'drawnTv', 'drawnBrowse', 'drawnRead', 'drawnPhone', 'albumResponse'];
function fixture(t: TestContext) {
  const base = mkdtempSync(join(tmpdir(), 'gallery-input-test-'));
  t.after(() => rmSync(base, {recursive: true, force: true}));
  const root = join(base, 'private'), repo = join(base, 'public'), app = join(repo, 'web');
  mkdirSync(root); mkdirSync(join(app, 'src/dev/families'), {recursive: true});
  writeFileSync(join(root, 'example.html'), '<h1>Public synthetic example</h1>');
  const families = {layout: Object.fromEntries(layout.map(key => [key, 'example.html'])), browse: Object.fromEntries(browse.map(key => [key, 'example.html']))};
  const manifest = join(base, 'inputs.json');
  const write = (value: unknown) => writeFileSync(manifest, JSON.stringify(value));
  write({version: 1, root, families});
  return {base, root, repo, app, manifest, families, write};
}

test('external DEV carrier reads exactly the requested family, with no default input', t => {
  const f = fixture(t);
  assert.deepEqual(Object.keys(readGalleryInputs(f.manifest, 'layout', f.app)), layout);
  // An absent unrelated family does not prevent the selected real family loading.
  f.write({version: 1, root: f.root, families: {browse: f.families.browse}});
  assert.deepEqual(Object.keys(readGalleryInputs(f.manifest, 'browse', f.app)), browse);
  assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  assert.throws(() => readGalleryInputs(undefined, 'browse', f.app));
  assert.throws(() => readGalleryInputs(f.manifest, 'controls', f.app));
});

test('carrier rejects public roots, traversal, symlink escapes, extra keys and oversized/non-UTF8 files', t => {
  const f = fixture(t);
  const writeLayout = (files: Record<string, string>, root = f.root) => f.write({version: 1, root, families: {layout: files}});
  writeLayout(f.families.layout, f.repo); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  writeLayout(f.families.layout, f.base); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  const outside = join(f.base, 'outside.html'); writeFileSync(outside, 'not a carrier input');
  symlinkSync(outside, join(f.root, 'escape.html'));
  for (const name of ['../outside.html', outside, 'escape.html', 'missing.html', 'example.html?raw']) {
    writeLayout({...f.families.layout, referenceHtml: name}); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  }
  writeLayout({...f.families.layout, extra: 'example.html'}); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  writeFileSync(join(f.root, 'large.html'), Buffer.alloc(4 * 1024 * 1024 + 1));
  writeLayout({...f.families.layout, referenceHtml: 'large.html'}); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  writeFileSync(join(f.root, 'invalid.html'), Buffer.from([0xff]));
  writeLayout({...f.families.layout, referenceHtml: 'invalid.html'}); assert.throws(() => readGalleryInputs(f.manifest, 'layout', f.app));
  writeLayout(f.families.layout);
  const publicManifest = join(f.repo, 'inputs.json'); writeFileSync(publicManifest, '{}');
  assert.throws(() => readGalleryInputs(publicManifest, 'layout', f.app));
});

test('DEV endpoint is per-family, same-origin, no-store and redacts missing private inputs', t => {
  const f = fixture(t), previous = process.env.PORTICO_GALLERY_INPUT_MANIFEST;
  process.env.PORTICO_GALLERY_INPUT_MANIFEST = f.manifest;
  t.after(() => { if (previous === undefined) delete process.env.PORTICO_GALLERY_INPUT_MANIFEST; else process.env.PORTICO_GALLERY_INPUT_MANIFEST = previous; });
  type Request = {url: string; method?: string; headers?: Record<string, string>};
  type Response = {statusCode: number; setHeader(key: string, value: string): void; end(body: string): void};
  let handler: (request: Request, response: Response, next: () => void) => void;
  const plugin = kitFamilies(); plugin.configResolved({root: f.app, command: 'serve'});
  const watcher = {add() {}, on() { return watcher; }};
  plugin.configureServer({middlewares: {use(value: typeof handler) { handler = value; }}, watcher});
  const request = (url: string, method = 'GET', headers: Record<string, string> = {host: '127.0.0.1:5188'}) => {
    const result = {statusCode: 200, body: '', next: false, headers: {} as Record<string, string>, setHeader(key: string, value: string) { this.headers[key] = value; }, end(body: string) { this.body = body; }};
    handler({url, method, headers}, result, () => { result.next = true; }); return result;
  };
  const success = request('/__portico_gallery_inputs/layout');
  assert.equal(success.statusCode, 200); assert.deepEqual(Object.keys(JSON.parse(success.body)), layout);
  assert.equal(success.headers['Cache-Control'], 'no-store'); assert.equal(success.headers['X-Content-Type-Options'], 'nosniff');
  assert.equal(request('/src/dev/families/controls.tsx').next, true);
  for (const suffix of ['unknown', '../layout', 'layout?file=example.html', '%6cayout']) assert.equal(request('/__portico_gallery_inputs/' + suffix).statusCode, 404);
  assert.equal(request('/__portico_gallery_inputs/layout', 'POST').statusCode, 404);
  assert.equal(request('/__portico_gallery_inputs/layout', 'GET', {host: '127.0.0.1:5188', origin: 'https://other.invalid'}).statusCode, 403);
  assert.equal(request('/__portico_gallery_inputs/layout', 'GET', {host: '127.0.0.1:5188', 'sec-fetch-site': 'cross-site'}).statusCode, 403);
  assert.equal(request('/__portico_gallery_inputs/layout', 'GET', {host: 'rebinding.invalid:5188', origin: 'http://rebinding.invalid:5188'}).statusCode, 403);
  assert.equal(request('/__portico_gallery_inputs/layout', 'GET', {}).statusCode, 403);
  assert.equal(request('/__portico_gallery_inputs/layout', 'GET', {host: '[::1]:5188'}).statusCode, 200);
  process.env.PORTICO_GALLERY_INPUT_MANIFEST = join(f.base, 'missing-private-input.json');
  const failure = request('/__portico_gallery_inputs/layout');
  assert.equal(failure.statusCode, 503); assert.deepEqual(JSON.parse(failure.body), {error: 'gallery inputs unavailable'});
  assert.ok(!failure.body.includes(f.base));
});

test('the existing virtual module loads inputs only on demand and production never fetches', async t => {
  const f = fixture(t), previous = globalThis.fetch, calls: string[] = [];
  t.after(() => { globalThis.fetch = previous; });
  globalThis.fetch = async url => { calls.push(String(url)); return new Response(JSON.stringify(Object.fromEntries(layout.map(key => [key, 'synthetic'])))); };
  const plugin = kitFamilies(); plugin.configResolved({root: f.app, command: 'serve'});
  const source = plugin.load('\0virtual:portico-kit-families');
  const module = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
  assert.equal(calls.length, 0);
  assert.deepEqual(Object.keys(await module.loadGalleryInputs('layout')), layout);
  assert.deepEqual(calls, ['/__portico_gallery_inputs/layout']);
  await assert.rejects(module.loadGalleryInputs('unknown')); assert.equal(calls.length, 1);
  globalThis.fetch = async () => new Response('{}');
  await assert.rejects(module.loadGalleryInputs('layout'));
  plugin.configResolved({root: f.app, command: 'build'});
  const production = plugin.load('\0virtual:portico-kit-families');
  assert.doesNotMatch(production, /fetch|__portico_gallery_inputs|manifest|referenceHtml/);
  const built = await import('data:text/javascript;base64,' + Buffer.from(production).toString('base64'));
  assert.deepEqual(built.default, []); await assert.rejects(built.loadGalleryInputs('layout'));
});
