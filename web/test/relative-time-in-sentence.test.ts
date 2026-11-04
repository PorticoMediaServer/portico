import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '../../packages/i18n/src/index.ts';

const now = Date.parse('2026-11-04T12:00:00Z'), justNow = now - 10_000, yesterday = now - 86_400_000;
const inSentence = (at: number) => defaultI18n.relativeTime(at, now, {inSentence: true});

test('every sentence that carries a relative time reads it lower-case (#600 sibling sweep)', () => {
  assert.equal(defaultI18n.t('api.credential.lastUsed', {when: inSentence(justNow)}), 'Last used just now');
  assert.equal(defaultI18n.t('next.settings.hardware.lastTest', {when: inSentence(yesterday)}), 'Tested yesterday.');
  assert.equal(defaultI18n.t('web.servers.checked', {when: inSentence(justNow)}), 'Checked just now');
  assert.equal(defaultI18n.t('web.hostedAccount.lastUsed', {when: inSentence(yesterday)}), 'last used yesterday');
  assert.equal(defaultI18n.t('next.remoteSources.checkedAt', {when: inSentence(justNow)}), 'Tested just now.');
  assert.equal(defaultI18n.t('next.remoteSources.libraryComplete', {when: inSentence(yesterday)}), 'last complete yesterday');
});

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
test('the six call sites ask for the in-sentence form; standalone labels keep their capital', () => {
  assert.match(source('../src/screens/settings/server/integrations/ApiKeys.tsx'), /relativeTime\(key\.lastUsedAt, undefined, \{inSentence: true\}\)/);
  assert.match(source('../src/screens/settings/hardware/hardware-rows.ts'), /relativeTime\(at, serverNow\(\), \{inSentence: true\}\)/);
  assert.match(source('../../apps/native/src-next/screens/settings-live/LiveOwnerScreen.tsx'), /relativeTime\(state\.checkedAt, undefined, \{inSentence: true\}\)/);
  assert.match(source('../../apps/native/src-next/screens/accounts/PhoneAccountWorkspace.tsx'), /relativeTime\(row\.lastUsedAt, undefined, \{inSentence: true\}\)/);
  const panel = source('../../apps/native/src-next/screens/settings/remote-sources/SourceStatusPanel.tsx');
  assert.match(panel, /const checked = ago\(status\?\.lastCheckedAt, true\);/);
  assert.match(panel, /libraryComplete', \{when: ago\(library\.lastCompleteAt, true\)\}/);
  // "Last error: Yesterday" and "Last connected: Just now" are standalone values.
  assert.match(panel, /const failedAt = ago\(status\?\.lastErrorAt\), connectedAt = ago\(status\?\.lastConnectedAt\);/);
  // Notification times are standalone labels too.
  assert.match(source('../src/screens/account/Notifications.route.tsx'), /text: defaultI18n\.relativeTime\(note\.createdAt\)\}/);
  assert.equal(defaultI18n.relativeTime(justNow, now), 'Just now');
});
