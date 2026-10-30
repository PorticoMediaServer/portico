import test from 'node:test';
import assert from 'node:assert/strict';
import {isSettingsDestination, settingsDestinations, serverSettingsDestinations} from '../src/settings-destinations.ts';
import {ACCOUNT_PROFILE_LIMIT, ACCOUNT_PROFILE_READ_LIMIT} from '../src/profile-limits.ts';
import {porticoPublicLinks} from '../src/public-links.ts';

test('settings destinations accept only the stable list', () => {
  for (const d of settingsDestinations) assert.equal(isSettingsDestination(d), true, d);
  assert.equal(isSettingsDestination('m3u'), false, 'never a source-type name');
  assert.equal(isSettingsDestination(''), false);
  assert.equal(isSettingsDestination(null), false);
  assert.equal(isSettingsDestination(undefined), false);
  assert.equal(isSettingsDestination(42), false);
  assert.equal(isSettingsDestination('Personal'), false, 'case-sensitive');
  for (const d of serverSettingsDestinations) assert.ok((settingsDestinations as readonly string[]).includes(d), d);
});

test('account constants: creation cap below the defensive read bound', () => {
  assert.equal(ACCOUNT_PROFILE_LIMIT, 8);
  assert.ok(ACCOUNT_PROFILE_READ_LIMIT >= ACCOUNT_PROFILE_LIMIT);
  assert.equal(ACCOUNT_PROFILE_READ_LIMIT, 100);
});

test('public links are canonical https destinations with a trailing slash', () => {
  assert.ok(porticoPublicLinks.terms.startsWith('https://'));
  assert.ok(porticoPublicLinks.privacy.startsWith('https://'));
  assert.ok(porticoPublicLinks.terms.endsWith('/'));
  assert.ok(porticoPublicLinks.privacy.endsWith('/'));
  assert.ok(!porticoPublicLinks.terms.includes('m3u'));
});
