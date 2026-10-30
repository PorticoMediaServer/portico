/**
 * Settings › About › Open-source licenses: the three generated lists (Android, Apple, Vega) must
 * match the inventories they were generated from, so they cannot silently drift. When this
 * fails: `node --experimental-strip-types apps/native/scripts/licences.mts` from the repository
 * root (with `--record` first if a lockfile changed, so the JavaScript bundles are read again).
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {existsSync, readdirSync, readFileSync} from 'node:fs';
import {lockPackages, readDependencyList, readPodfileLock} from '../../../packages/client-core/src/licences/inventory.ts';
import {licenceSections, parseLicenceData, type LicenceData} from '../../../packages/client-core/src/presentation/licences.ts';

const root = new URL('../../../', import.meta.url);
const at = (rel: string) => new URL(rel, root);
const read = (rel: string) => readFileSync(at(rel), 'utf8');
const lists = {
  android: 'apps/native/licences/generated/android.json',
  apple: 'apps/native/licences/generated/apple.json',
  vega: 'apps/vega/src/licences/vega.json',
} as const;
const load = (rel: string) => JSON.parse(read(rel)) as LicenceData;
const REGENERATE = 'run: node --experimental-strip-types apps/native/scripts/licences.mts (with --record after a lockfile change)';

for (const [client, file] of Object.entries(lists)) {
  test(`${client}: every inventory the list was generated from is unchanged`, () => {
    const data = load(file);
    assert.ok(Object.keys(data.inputs).length > 3);
    for (const [rel, digest] of Object.entries(data.inputs)) {
      const now = createHash('sha256').update(readFileSync(at(rel))).digest('hex');
      assert.equal(now, digest, `${rel} changed since ${file} was generated; ${REGENERATE}`);
    }
  });

  test(`${client}: well formed, Portico first, every component with its text or a reason`, () => {
    const data = load(file);
    assert.ok(parseLicenceData(data));
    assert.equal(data.client, client);
    assert.equal(data.components[0]!.id, 'portico');
    assert.match(data.texts[data.components[0]!.text!]!, /GNU GENERAL PUBLIC LICENSE\s+Version 3/);
    assert.equal(data.sources[0]!.id, 'portico-apps');
    for (const c of data.components) {
      assert.ok(c.text === undefined || typeof data.texts[c.text] === 'string', c.id);
      assert.ok(c.text !== undefined || c.textPending || (c.textAtUrl && c.url), `${c.id} has no licence text`);
    }
    assert.equal(new Set(data.texts).size, data.texts.length, 'identical texts are stored once');
    for (const id of ['font:manrope', 'icons:lucide']) assert.ok(data.components.some(c => c.id === id), id);
  });
}

test('the recorded JavaScript bundles name exactly the locked versions', () => {
  for (const [list, app] of [['js-android', 'apps/native'], ['js-ios', 'apps/native'], ['js-vega', 'apps/vega']] as const) {
    const lock = lockPackages(JSON.parse(read(`${app}/package-lock.json`)));
    const lines = read(`apps/native/licences/${list}.txt`).split('\n').filter(l => l && !l.startsWith('#'));
    assert.ok(lines.length > 20, list);
    for (const line of lines) {
      const [nameVersion, path] = line.split('\t');
      const entry = lock.get(path!);
      assert.equal(entry ? `${entry.name}@${entry.version}` : undefined, nameVersion, `${list}: ${line}; ${REGENERATE}`);
    }
  }
  // What the bundles hold, not the whole lockfile: tooling never appears, React Native always does.
  const android = read('apps/native/licences/js-android.txt');
  assert.match(android, /^react-native(-tvos)?@/m);
  assert.doesNotMatch(android, /^(metro|typescript|eslint|@babel\/core)@/m);
});

test('the Android list carries every pinned release library of both builds, Cast only in the Google one', () => {
  const data = load(lists.android);
  for (const flavour of ['google', 'firetv']) {
    for (const d of readDependencyList(read(`apps/native/licences/android-${flavour}.txt`))) {
      const c = data.components.find(x => x.id === `maven:${d.group}:${d.artifact}:${d.version}`);
      assert.ok(c, `${d.group}:${d.artifact}:${d.version}`);
      assert.ok(!c!.only || c!.only.includes(flavour), c!.id);
    }
  }
  const cast = data.components.find(c => c.name === 'com.google.android.gms:play-services-cast-framework')!;
  assert.deepEqual(cast.only, ['google']);
  assert.ok(licenceSections(data, 'firetv').every(s => s.components.every(c => !c.name.includes('play-services'))));
});

test('each platform lists its own shipped native recipe; only Android carries mpv and libdovi crates', () => {
  for (const [client, platform] of [['android', 'android'], ['apple', 'apple']] as const) {
    const recipe = platform === 'apple' ? 'third_party/apple-subtitles' : 'third_party/media-engine';
    const lock = JSON.parse(read(`${recipe}/sources.lock.json`)) as {components: Record<string, {version: string; platforms: string[]}>};
    const data = load(lists[client]);
    for (const [name, entry] of Object.entries(lock.components)) {
      const listed = data.components.some(c => c.id === `engine:${name}@${entry.version}`);
      assert.equal(listed, entry.platforms.includes(platform), `${client}: ${name}`);
    }
    assert.equal(data.components.some(c => c.id.startsWith('crate:') && c.group === 'engine'), platform === 'android', client);
    if (platform === 'apple') {
      assert.deepEqual(data.components.filter(c => c.group === 'engine').map(c => c.id).sort(), Object.entries(lock.components).map(([name, entry]) => `engine:${name}@${entry.version}`).sort());
      assert.ok(!data.components.some(c => /mpv|moltenvk|glslang|libdovi/i.test(c.id)));
      assert.ok(!('third_party/media-engine/sources.lock.json' in data.inputs));
      assert.ok('third_party/apple-subtitles/jellyfin-ffmpeg.source.lock.json' in data.inputs);
    }
    assert.ok(data.sources.some(s => s.id === 'media-engine'), client);
  }
  assert.ok(!load(lists.vega).components.some(c => c.group === 'engine'));
});

test('every pod that is not React Native’s generated code or an npm package of the bundle is listed', () => {
  const data = load(lists.apple);
  const names = new Set(data.components.map(c => c.name));
  for (const variant of ['ios', 'tvos']) {
    for (const pod of readPodfileLock(read(`apps/native/${variant}/Podfile.lock`))) {
      if (/^(React|RCT|FBLazyVector|Yoga|ReactCommon|ReactCodegen|ReactAppDependencyProvider|RN|react-native-)/.test(pod.name)) continue;
      assert.ok(names.has(pod.name) || names.has(pod.name === 'hermes-engine' ? 'Hermes' : pod.name), `${variant}: ${pod.name}`);
    }
  }
  assert.deepEqual(data.components.find(c => c.name === 'google-cast-sdk')?.only, ['ios']);
});

test('every font the apps carry is in the asset inventory', () => {
  const assets = JSON.parse(read('apps/native/licences/assets.json')) as {assets: {files: string[]; licenceFile: string}[]};
  const listed = new Set(assets.assets.flatMap(a => a.files));
  for (const name of readdirSync(at('apps/native/assets/'))) {
    if (/\.(ttf|otf|woff2?)$/i.test(name)) assert.ok(listed.has(`apps/native/assets/${name}`), name);
  }
  for (const a of assets.assets) assert.ok(existsSync(at(a.licenceFile)), a.licenceFile);
});

test('the lists are loaded only by the licence screens, never at start', () => {
  for (const loader of ['apps/native/src-next/platform/licences.ts', 'apps/native/src-next/platform/licences.android.ts', 'apps/vega/src/overrides/native-next/platform/licences.ts']) {
    const source = read(loader);
    // A require inside the function: Metro evaluates the list's module only when it is called.
    assert.match(source, /export function loadLicences\(\)[^{]*\{\s*(\/\/[^\n]*\n\s*)?return parseLicenceData\(require\(/, loader);
  }
  const importers = (dir: string): string[] => readdirSync(at(dir), {withFileTypes: true}).flatMap(e => e.isDirectory() ? importers(`${dir}${e.name}/`) : /\.tsx?$/.test(e.name) && /from ['"][^'"]*platform\/licences['"]/.test(read(`${dir}${e.name}`)) ? [`${dir}${e.name}`] : []);
  assert.deepEqual(importers('apps/native/src-next/'), ['apps/native/src-next/screens/settings-live/NativeLicencesScreen.tsx']);
});
