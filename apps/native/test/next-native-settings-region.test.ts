import assert from 'node:assert/strict';
import test from 'node:test';
import {createI18n, defaultI18n, resolveRegion} from '../../../packages/i18n/src';
import {parsePreferenceSnapshot, type PreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {regionPreferences} from '../../../packages/client-core/src/preference-effects';
import {nativeRuntimeRegionText} from '../src-next/screens/settings/runtime-region';
import {nativeGuideRegion, nativeGuideClock} from '../src-next/screens/settings-live/guide-presentation';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const device = {locales: ['en-CA'], timeZone: 'UTC'};
const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
function preferences(locale = 'en-US', formats = 'en-US', revision = 1, hourCycle = 'auto'): PreferenceSnapshot {
  const effective = {
    'playback.defaultSpeed': 1, 'audiobooks.defaultSpeed': 1, 'playback.skipBackMs': 10000, 'playback.skipForwardMs': 30000,
    'playback.introSkip': 'ask', 'playback.recapSkip': 'ask', 'playback.creditsSkip': 'off',
    'playback.subtitleSize': 'medium', 'playback.subtitleBackground': 'translucent',
    'region.locale': locale, 'region.formats': formats, 'region.hourCycle': hourCycle, 'region.dateOrder': 'auto', 'region.firstDay': 'auto', 'region.sizeUnits': 'binary',
  };
  return parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'region-fixture', registry: {revision: 'region-fixture',
    fields: Object.entries(effective).map(([key, value]) => ({key, type: key === 'region.locale' ? 'locale' : typeof value === 'number' ? 'number' : 'string',
      default: value, group: key.split('.')[0], labelKey: `preferences.${key}`, scopes: ['profile-server', 'profile-device-class']}))},
    documents: [{scope: 'profile-server', revision, activeRevision: revision, digest: 'd'.repeat(64), values: effective},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'e'.repeat(64), values: {}}],
    effective, effectiveSource: Object.fromEntries(Object.keys(effective).map(key => [key, 'profile-server'])), clampedFields: []});
}

test('runtime text borrows confirmed regional values and the canonical formatter without mutating its source', () => {
  let snapshot = preferences('auto', 'auto'), current = true, deviceReads = 0;
  const text = nativeRuntimeRegionText({current: () => current, preferences: () => snapshot}, () => {deviceReads++; return device;});
  const initial = snapshot, expected = createI18n(resolveRegion(regionPreferences(snapshot), device));
  assert.equal(text.locale, expected.locale);
  assert.equal(text.t('next.kit.items', {count: 1234}), expected.t('next.kit.items', {count: 1234}));
  assert.equal(deviceReads, 1, 'the same confirmed snapshot reuses its formatter');
  snapshot = preferences('en-GB', 'de-DE', 2);
  const next = createI18n(resolveRegion(regionPreferences(snapshot), device));
  assert.equal(text.locale, 'en-GB');
  assert.equal(text.t('next.kit.items', {count: 1234}), next.t('next.kit.items', {count: 1234}));
  assert.equal(initial.effective['region.locale'], 'auto');
  current = false;
  assert.throws(() => text.locale, {name: 'AbortError'});
  assert.throws(() => text.t('next.kit.items', {count: 1}), {name: 'AbortError'});
});

test('an older partial registry keeps existing runtime text and never invents missing regional defaults', () => {
  const complete = preferences();
  const partial = {...complete, registry: {...complete.registry, fields: complete.registry.fields.filter(field => field.key !== 'region.formats')}};
  let deviceReads = 0;
  const text = nativeRuntimeRegionText({current: () => true, preferences: () => partial}, () => {deviceReads++; return device;});
  assert.equal(text.locale, defaultI18n.locale); assert.equal(text.t, defaultI18n.t);
  assert.equal(deviceReads, 0);
  assert.equal(text.refreshDeviceRegion(), false); assert.equal(deviceReads, 0);
});

test('auto regional text refreshes a changed device without a changed preference snapshot or a new observer', () => {
  const confirmed = preferences('auto', 'auto');
  let actualDevice = device, current = true;
  const text = nativeRuntimeRegionText({current: () => current, preferences: () => confirmed}, () => actualDevice);
  assert.equal(text.locale, 'en-CA'); const initial = text.t;
  assert.equal(text.refreshDeviceRegion(), false); assert.equal(text.t, initial);
  actualDevice = {locales: ['en-GB'], timeZone: 'Europe/London'};
  assert.equal(text.refreshDeviceRegion(), true); assert.equal(text.locale, 'en-GB');
  const expected = createI18n(resolveRegion(regionPreferences(confirmed), actualDevice));
  assert.equal(text.t('next.kit.items', {count: 1234}), expected.t('next.kit.items', {count: 1234}));
  assert.equal(confirmed.effective['region.locale'], 'auto');
  current = false; assert.throws(text.refreshDeviceRegion, {name: 'AbortError'});
});

test('explicit regional choices keep their formatter when an irrelevant device locale changes', () => {
  let actualDevice = device;
  const confirmed = preferences('en-US', 'de-DE');
  const text = nativeRuntimeRegionText({current: () => true, preferences: () => confirmed}, () => actualDevice);
  const translate = text.t;
  actualDevice = {...actualDevice, locales: ['en-GB']};
  assert.equal(text.refreshDeviceRegion(), false); assert.equal(text.t, translate); assert.equal(text.locale, 'en-US');
});

test('a real Settings ACK republishes bootstrap text only after confirmation and retirement closes the borrowed text port', async () => {
  let confirmed = preferences(), rejectWrite = false;
  const begun = deferred(), release = deferred();
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    if (!request.path.startsWith('/v1/preferences')) return;
    if (request.method === 'PATCH') {
      assert.equal(request.body.expectedRevision, confirmed.documents[0]!.revision);
      if (rejectWrite) return {status: 409, body: {error: {code: 'preference_conflict', message: 'region save refused'}}};
      begun.resolve(); await release.promise;
      confirmed = preferences(request.body.values['region.locale'], request.body.values['region.formats'], confirmed.documents[0]!.revision + 1);
    }
    return {status: 200, body: {scope: {serverId: fixture.pin.serverId, viewerFence: 'a'.repeat(64)}, data: confirmed}};
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {nativeServerPreferences} = await import('../src-next/platform/server-preferences');
    let captured: NativeShellBootstrapContext | undefined;
    runtime = await restoreNativeShellRuntime(context => {captured = context; return createNativeShellBindings(context);}, {onAuthorityEnded() {throw new Error('unexpected_retirement');}});
    assert.ok(runtime); assert.ok(captured);
    const source = nativeServerPreferences(captured), before = runtime.getSnapshot(), oldText = runtime.text.t('next.kit.items', {count: 1234});
    let publications = 0; const off = runtime.subscribe(() => {publications++;});
    const writing = source.applyPreferences('profile-server', 'mobile', 1, {'region.locale': 'en-GB', 'region.formats': 'de-DE'});
    await begun.promise;
    assert.equal(runtime.text.locale, 'en-US'); assert.equal(runtime.getSnapshot(), before);
    release.resolve(); await writing;
    assert.equal(runtime.text.locale, 'en-GB'); assert.notEqual(runtime.text.t('next.kit.items', {count: 1234}), oldText);
    assert.notEqual(runtime.getSnapshot(), before); assert.ok(publications > 0);
    assert.equal(captured.preferences(), source.getSnapshot());
    rejectWrite = true;
    await assert.rejects(source.applyPreferences('profile-server', 'mobile', 2, {'region.locale': 'en-US'}), {code: 'preference_conflict'});
    assert.equal(runtime.text.locale, 'en-GB');
    off(); runtime.dispose();
    assert.throws(() => runtime!.text.locale, {name: 'AbortError'});
  } finally {
    try {
      release.resolve();
    } finally {
      try {
        runtime?.dispose();
      } finally {
        try {
          await app.unmount();
        } finally {
          app.disposeLoader();
        }
      }
    }
  }
});


test('Guide clock borrows the same confirmed format locale and hour-cycle independently of UI language', () => {
  let confirmed = preferences('en-US', 'de-DE', 1, 'h23'), current = true;
  const text = nativeRuntimeRegionText({current: () => current, preferences: () => confirmed}, () => device);
  const original = text.region;
  assert.equal(text.locale, 'en-US'); assert.equal(text.region, original, 'one memoized formatter region');
  assert.deepEqual(nativeGuideRegion(text), {locale: 'de-DE', hour12: false});
  const instant = Date.UTC(2026, 9, 24, 13, 5);
  assert.equal(nativeGuideClock(nativeGuideRegion(text).locale, 'UTC', nativeGuideRegion(text).hour12, false)(instant), '13:05');
  confirmed = preferences('en-GB', 'en-US', 2, 'h12');
  assert.notEqual(text.region, original); assert.equal(text.locale, 'en-GB');
  const next = nativeGuideRegion(text);
  assert.deepEqual(next, {locale: 'en-US', hour12: true});
  assert.equal(nativeGuideClock(next.locale, 'UTC', next.hour12, false)(instant), '1:05 PM');
  current = false; assert.throws(() => text.region, {name: 'AbortError'});
});

test('Guide preserves genuine auto-hour resolution and the previous old-runtime fallback', () => {
  const text = nativeRuntimeRegionText({current: () => true, preferences: () => preferences('en-US', 'en-GB')}, () => device);
  assert.equal(nativeGuideRegion(text).hour12, text.region.hour12);
  assert.deepEqual(nativeGuideRegion({locale: 'en-CA'}), {locale: 'en-CA', hour12: defaultI18n.region.hour12});
});
