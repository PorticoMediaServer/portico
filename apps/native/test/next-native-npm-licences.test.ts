import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {splitCopyright} from '../../../packages/client-core/src/licences/inventory.ts';
import type {LicenceData} from '../../../packages/client-core/src/presentation/licences.ts';
import {nativeNpmComponents, type NativeNpmEntry, type NativeClient} from '../scripts/native-npm-licences.mts';

const text = 'MIT fixture text';
const entry: NativeNpmEntry = {name: 'native-blur', version: '1.0.0', platforms: ['android', 'apple'], licence: 'MIT', licenceFile: 'LICENSE', licenceSHA256: createHash('sha256').update(text).digest('hex'), url: 'https://example.test/native-blur'};
const linked = new Map<string, NativeClient[]>([['native-blur', ['android', 'apple']]]);
const installed = () => ({version: '1.0.0', license: 'MIT', repository: {url: 'git+https://example.test/native-blur.git'}});
const collect = (entries: NativeNpmEntry[], represented = new Set<string>(), links = linked) => nativeNpmComponents('android', entries, links, represented, installed, () => text);

test('native-only inventory uses actual package/version/licence provenance and native platform category', () => {
  const result = collect([entry]); assert.equal(result.length, 1); assert.equal(result[0]!.group, 'android'); assert.equal(result[0]!.text, text);
  assert.equal(nativeNpmComponents('apple', [entry], linked, new Set(), installed, () => text)[0]!.group, 'apple');
  assert.throws(() => collect([{...entry, version: '2.0.0'}]), /provenance changed/);
  assert.throws(() => collect([{...entry, licenceSHA256: 'bad'}]), /provenance changed/);
  assert.throws(() => collect([{...entry, url: 'https://example.test/another-package'}]), /provenance changed/);
  assert.throws(() => collect([{...entry, licenceFile: '../LICENSE'}]), /provenance/);
  assert.throws(() => collect([{...entry, licence: 'SSPL'}]), /Unsupported/);
});
test('actual JS/pod representation deduplicates native identity and duplicate inventory fails', () => {
  assert.deepEqual(collect([entry], new Set(['native-blur'])), []);
  assert.throws(() => collect([entry, entry]), /Duplicate native npm registration/);
});
test('a genuinely shipped unlisted native module fails while an unshipped installed module does not', () => {
  assert.throws(() => collect([]), /Shipped native npm module is unlisted/);
  assert.deepEqual(collect([], new Set(), new Map()), []);
  assert.throws(() => collect([entry], new Set(), new Map()), /not autolinked/);
});
test('platform scope cannot register an Android-only library for Apple', () => {
  const androidOnly = new Map<string, NativeClient[]>([['native-blur', ['android']]]);
  assert.throws(() => collect([entry], new Set(), androidOnly), /not autolinked/);
  const scoped = {...entry, platforms: ['android' as const]};
  assert.equal(collect([scoped], new Set(), androidOnly).length, 1);
  assert.deepEqual(nativeNpmComponents('apple', [scoped], androidOnly, new Set(), installed, () => text), []);
});

test('generated native licences include required blur, mask and gestures once and the exact upstream licences', () => {
  const inventory = JSON.parse(readFileSync(new URL('../licences/native-npm.json', import.meta.url), 'utf8')) as NativeNpmEntry[];
  for (const client of ['android', 'apple'] as const) {
    const graph = readFileSync(new URL(`../licences/js-${client === 'android' ? 'android' : 'ios'}.txt`, import.meta.url), 'utf8');
    const represented = new Set(graph.split('\n').filter(line => line.trim() && !line.startsWith('#')).map(line => line.split('\t')[0]));
    const data = JSON.parse(readFileSync(new URL(`../licences/generated/${client}.json`, import.meta.url), 'utf8')) as LicenceData;
    for (const [name, version] of [['@react-native-community/blur', '4.4.1'], ['@react-native-masked-view/masked-view', '0.3.2'], ['react-native-gesture-handler', '2.30.0']]) {
      const entries = data.components.filter(component => component.name === name);
      assert.equal(entries.length, 1); assert.equal(entries[0]!.version, version); assert.equal(entries[0]!.licence, 'MIT');
      const component = entries[0]!;
      const bundled = represented.has(`${name}@${version}`);
      assert.equal(component.group, bundled ? 'packages' : client);
      assert.equal(component.id, `${bundled ? 'npm' : 'native-npm'}:${name}@${version}`);
      const registration = inventory.find(item => item.name === name)!;
      assert.equal(registration.version, version);
      const upstream = readFileSync(new URL(`../node_modules/${name}/${registration.licenceFile}`, import.meta.url), 'utf8');
      assert.equal(createHash('sha256').update(upstream).digest('hex'), registration.licenceSHA256);
      assert.equal(typeof component.text, 'number');
      const licence = splitCopyright(upstream);
      assert.equal(data.texts[component.text!], licence.body);
      assert.deepEqual(component.copyright ?? [], licence.copyright);
      assert.equal(component.textPending, undefined);
    }
    if (client === 'android') {
      const maven = data.components.find(component => component.id === 'maven:com.github.Dimezis:BlurView:version-2.0.4')!;
      assert.equal(maven.licence, 'Apache-2.0'); assert.match(data.texts[maven.text!]!, /Apache License/);
      assert.equal(maven.textPending, undefined); assert.equal(maven.only, undefined);
    }
  }
});
