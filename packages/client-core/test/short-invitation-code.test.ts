import test from 'node:test';
import assert from 'node:assert/strict';
import {parseShortInvitationCode, formatShortInvitationCode} from '../src/short-invitation-code.ts';
import {parseJoinLink} from '../src/join-link.ts';

test('short invitation codes preserve the Server eighty-bit vectors and declared grouping', () => {
  for (const raw of ['0000000000000000', 'ZZZZZZZZZZZZZZZZ', '008J4CT4ANK7F24S', '008j4ct4ank7f24s', '008J-4CT4-ANK7-F24S', '008j-4ct4-ank7-f24s']) {
    const canonical = raw.replaceAll('-', '').toUpperCase();
    assert.equal(parseShortInvitationCode(raw), canonical);
    assert.equal(formatShortInvitationCode(raw), `${canonical.slice(0, 4)}-${canonical.slice(4, 8)}-${canonical.slice(8, 12)}-${canonical.slice(12)}`);
  }
});

test('ambiguous letters, extra separators, Unicode, long-link secrets and URLs are not short codes', () => {
  for (const raw of [undefined, null, 123, {}, '', '008J 4CT4 ANK7 F24S', '008J--4CT4-ANK7-F24S', '008J-4CT4ANK7-F24S', '008J-4CT4-ANK7-F24S-', ' 008J4CT4ANK7F24S', '008J4CT4ANK7F24S\n', '008J4CT4ANK7F24S\u200b', '００8J4CT4ANK7F24S', 'a'.repeat(43), 'https://server.test/#code=008J4CT4ANK7F24S']) {
    assert.equal(parseShortInvitationCode(raw), undefined);
    assert.equal(formatShortInvitationCode(raw), undefined);
  }
  for (const ambiguous of 'ILOUilou') assert.equal(parseShortInvitationCode(`008J4CT4ANK7F24${ambiguous}`), undefined);
  assert.equal(parseJoinLink('portico://join#code=008J4CT4ANK7F24S&server=https%3A%2F%2Fserver.test'), undefined, 'long-link contract stays distinct');
});
