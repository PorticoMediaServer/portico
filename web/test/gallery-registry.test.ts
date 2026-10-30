import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import type {GalleryFamily} from '../src/dev/gallery-registry.ts';

test('selected public gallery loads only its genuine family and retains registry validation', async t => {
  type Loader = (() => Promise<{default: GalleryFamily}>) & {familyId: string};
  const state = globalThis as typeof globalThis & {__galleryTestLoaders?: Loader[]};
  const loaders: Loader[] = [];
  state.__galleryTestLoaders = loaders;
  const url = 'data:text/javascript,export default globalThis.__galleryTestLoaders';
  const hook = registerHooks({resolve(specifier, context, next) {
    return specifier === 'virtual:portico-kit-families' ? {url, shortCircuit: true} : next(specifier, context);
  }});
  t.after(() => { hook.deregister(); delete state.__galleryTestLoaders; });
  const {loadGalleryFamilies} = await import('../src/dev/gallery-registry.ts');
  const family = (id: string): GalleryFamily => ({id, labelKey: 'next.platform.kitTitle', cases: [{id: 'primary', labelKey: 'next.platform.kitTitle', width: 390, height: 844, render: () => null}]});
  const calls: string[] = [];
  const add = (id: string, value = family(id)) => loaders.push(Object.assign(async () => { calls.push(id); return {default: value}; }, {familyId: id}));

  add('controls');
  loaders.push(Object.assign(async () => { throw new Error('Unselected private dependency must never load.'); }, {familyId: 'layout'}));
  assert.deepEqual((await loadGalleryFamilies('controls')).map(value => value.id), ['controls']);
  assert.deepEqual(calls, ['controls']);
  assert.deepEqual(await loadGalleryFamilies('unknown'), []);
  assert.deepEqual(await loadGalleryFamilies(''), []);
  assert.deepEqual(calls, ['controls']);

  loaders.splice(0); calls.splice(0);
  add('controls'); add('states');
  assert.deepEqual((await loadGalleryFamilies()).map(value => value.id), ['controls', 'states']);
  assert.deepEqual(calls, ['controls', 'states']);

  loaders.splice(0); add('controls', family('states'));
  await assert.rejects(loadGalleryFamilies('controls'), /does not match/);
  loaders.splice(0); add('controls'); add('controls');
  await assert.rejects(loadGalleryFamilies('controls'), /duplicate gallery family/);
  loaders.splice(0); add('controls', {...family('controls'), cases: [{...family('controls').cases[0], width: 0}]});
  await assert.rejects(loadGalleryFamilies('controls'), /gallery state/);
});
