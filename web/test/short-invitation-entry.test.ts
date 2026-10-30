import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import {formatShortInvitationCode, parseShortInvitationCode} from '@core/short-invitation-code.ts';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.svg')) return {format: 'module', source: 'export default "/wordmark.svg";', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {shortInvitationCodeAfterBlur, shortInvitationCodeForSubmit, shortInvitationCodePlaceholder, shortInvitationFormErrorKind, shortInvitationRefusal} = await import('../src/screens/accounts/ShortInvitationEntry.tsx');
const {LocalInvitationForm} = await import('../src/kit/accounts/Invitation.tsx');

test('the typed invitation field preserves the full input and leaves case, separators and long-link refusal to the checked codec', () => {
  assert.equal(shortInvitationCodePlaceholder, 'XXXX-XXXX-XXXX-XXXX');
  for (const raw of ['008J4CT4ANK7F24S', '008j4ct4ank7f24s', '008J-4CT4-ANK7-F24S', '008j-4ct4-ank7-f24s']) {
    assert.equal(shortInvitationCodeForSubmit(raw), raw);
    assert.equal(parseShortInvitationCode(shortInvitationCodeForSubmit(raw)), '008J4CT4ANK7F24S');
    assert.equal(shortInvitationCodeAfterBlur(raw), '008J-4CT4-ANK7-F24S');
  }
  for (const invalid of ['008J 4CT4 ANK7 F24S', '008J--4CT4-ANK7-F24S', '008J-4CT4ANK7-F24S', '008J-4CT4-ANK7-F24S-', '００8J4CT4ANK7F24S', '008J4CT4ANK7F24S\u200b', 'https://server.test/join#code=' + 'a'.repeat(43), '008J4CT4ANK7F24SXYZ']) {
    assert.equal(shortInvitationCodeForSubmit(invalid), invalid);
    assert.equal(shortInvitationCodeAfterBlur(invalid), invalid);
    assert.equal(parseShortInvitationCode(shortInvitationCodeForSubmit(invalid)), undefined);
    assert.equal(formatShortInvitationCode(shortInvitationCodeForSubmit(invalid)), undefined);
  }
});

test('only the checked invitation refusal codes get the generic sentence; auth, identity and rate errors stay typed', () => {
  for (const code of ['invitation_not_found', 'invalid_request', 'invitation_expired', 'invitation_used', 'invitation_for_another_account']) {
    assert.equal(shortInvitationRefusal({code}), true);
    assert.equal(shortInvitationFormErrorKind('password', {code}, undefined, false), 'unavailable');
  }
  for (const error of [{code: 'identity_mismatch'}, {code: 'rate_limited'}, {code: 'unauthorized'}, {status: 401}, {status: 403}, {status: 409}, {status: 429}, {status: 500}]) {
    assert.equal(shortInvitationRefusal(error), false);
    assert.equal(shortInvitationFormErrorKind('password', error, undefined, false), 'typed');
  }
  assert.equal(shortInvitationFormErrorKind('address', {code: 'invitation_incomplete'}, undefined, false), 'invalid');
  assert.equal(shortInvitationFormErrorKind('address', {code: 'invalid_address'}, undefined, false), 'typed');
  assert.equal(shortInvitationFormErrorKind('password', {code: 'invitation_not_found'}, {signInMethod: 'local'}, false), undefined);
  assert.equal(shortInvitationFormErrorKind('password', {code: 'invitation_not_found'}, undefined, true), undefined);
});

test('the acceptance form can show the one generic refusal instead of a code-specific sentence', () => {
  const html = renderToStaticMarkup(createElement(LocalInvitationForm, {serverName: 'Portico demo', libraries: [], error: {code: 'invitation_expired'}, errorContent: createElement('p', {role: 'alert'}, 'This invitation isn’t available. Ask the owner for a new code.'), onJoin() {}, onBack() {}}));
  assert.match(html, /This invitation isn’t available/);
  assert.doesNotMatch(html, /invitation_expired/);
});
