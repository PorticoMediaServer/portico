/** CON-18 (MU7): the loading and empty copy keys resolve to the §4.4 wording on every client. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {enUS, formatMessage} from '../../../packages/i18n/src/index.ts';

test('CON-18: loading and empty copy keys resolve to the §4.4 wording', () => {
  const table: [string, (v: string) => void][] = [
    ['status.loadingThing', v => assert.match(formatMessage(v, {thing: 'profiles'}), /^Loading profiles$/)],
    ['auth.loadingProfiles', v => assert.match(v, /^Loading .+/)],
    ['downloads.emptyBody', v => assert.match(v, /Press and hold/)],
    ['title.playlistEmptyBodyTV', v => assert.match(v, /Press and hold Select/)],
  ];
  for (const [key, check] of table) {
    const value = (enUS as Record<string, string>)[key];
    assert.ok(value, `${key} resolves`);
    assert.doesNotMatch(value, /…|\.\.\./, `${key} has no ellipsis`);
    assert.notEqual(value, 'Loading', `${key} is never a bare "Loading"`);
    check(value);
  }
});
