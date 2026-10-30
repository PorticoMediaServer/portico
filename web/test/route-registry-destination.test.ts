import test from 'node:test';
import assert from 'node:assert/strict';
import {createEntryDestinationStore, isProfileSettingsLocation, profileSettingsAliasPath, profileSettingsDestination} from '../src/route-registry-destination.ts';
function storage() {
  const values = new Map<string, string>();
  return {values, getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => {values.set(key, value);}, removeItem: (key: string) => {values.delete(key);}};
}
test('the ruling31 alias maps only to the existing account/profile Settings destination', () => {
  assert.equal(profileSettingsAliasPath, '/settings/profile');
  assert.deepEqual(profileSettingsDestination, {to: '/settings', search: {scope: 'account', section: 'profile'}});
  assert.equal(isProfileSettingsLocation({pathname: '/settings', search: {scope: 'account', section: 'profile'}}), true);
  assert.equal(isProfileSettingsLocation({pathname: '/settings', search: {scope: 'server', section: 'profile'}}), false);
});
test('a tab reload retains only a fixed public marker, never a username, session or arbitrary URL', () => {
  const saved = storage(), first = createEntryDestinationStore(() => saved);
  first.rememberProfileSettings();assert.deepEqual([...saved.values.values()], ['profile-settings']);
  const reloaded = createEntryDestinationStore(() => saved);
  assert.deepEqual(reloaded.take(), profileSettingsDestination);assert.equal(saved.values.size, 0);assert.equal(reloaded.take(), undefined);
});
test('unknown navigation storage is discarded instead of becoming a redirect', () => {
  const saved = storage(), destination = createEntryDestinationStore(() => saved);destination.rememberProfileSettings();
  const key = [...saved.values.keys()][0];saved.setItem(key, 'https://other.example/path');
  assert.equal(createEntryDestinationStore(() => saved).take(), undefined);assert.equal(saved.values.size, 0);
});
test('blocked session storage leaves a bounded in-memory destination without affecting authentication', () => {
  const destination = createEntryDestinationStore(() => {throw new Error('storage denied');});
  destination.rememberProfileSettings();assert.deepEqual(destination.take(), profileSettingsDestination);assert.equal(destination.take(), undefined);
});
