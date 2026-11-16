import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePreferenceSnapshot, type PreferenceScope} from '../../../packages/client-core/src/preferences';
import {bindNativeServerPreferences} from '../src-next/platform/server-preferences';
import {nativeReadingPreferencesAvailable, nativeReadingSettingsPreferences, nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';

// The rebuilt server keeps every reading setting per kind of device (10-reading rule 36). On a phone,
// every book crashed to "Something went wrong" because the app demanded them per profile instead.
function rebuiltServer(scope: PreferenceScope = 'profile-device-class') {
  const fields = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows).map(row => ({
    key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
    default: row.control.default, scopes: [scope], group: 'reading', labelKey: `preferences.${row.key}`,
    ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {}),
  }));
  return parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'r', registry: {revision: 'r', fields},
    documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
      {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
    effective: Object.fromEntries(fields.map(field => [field.key, field.default])), effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});
}

test('reading settings kept per kind of device are read and saved there, so a book opens', async () => {
  const snapshot = rebuiltServer(), saved: {scope: string; values: unknown}[] = [];
  assert.equal(nativeReadingPreferencesAvailable(snapshot), true);
  const context = {authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'e', viewerFence: 'a'.repeat(64)}), current: () => true,
    preferences: () => snapshot, subscribeAuthority: () => () => {}, subscribePreferences: () => () => {}, api: {} as never};
  bindNativeServerPreferences(context as never, {read: async () => snapshot, accept: next => next,
    apply: async (scope, _deviceClass, values) => {saved.push({scope, values}); return snapshot;}});
  const source = nativeReadingSettingsPreferences(context as never);
  assert.equal(source.getSnapshot().theme, 'dark');
  await source.setRow('reading.textSize', 6);
  assert.deepEqual(saved, [{scope: 'profile-device-class', values: {'reading.textSize': 6}}]);
});

test('a server that keeps them per profile still works, and a television never reads', () => {
  assert.equal(nativeReadingPreferencesAvailable(rebuiltServer('profile-server')), true);
  assert.equal(nativeReadingPreferencesAvailable({...rebuiltServer(), deviceClass: 'television'}), false);
});
