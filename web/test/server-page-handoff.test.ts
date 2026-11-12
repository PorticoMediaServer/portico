// A Portico Accounts server's own page hands signing in and joining on to web.getportico.tv
// (`platform/portico-handoff.ts`), and web.getportico.tv keeps a handed invitation through the
// account's sign-in (`screens/accounts/ChooserStart.tsx`).
import test from 'node:test';
import assert from 'node:assert/strict';
import {porticoHandoffUrl} from '../src/platform/portico-handoff.ts';
import {componentModule} from './helpers/component-harness.mjs';

const hostedOrigin = 'https://web.getportico.tv';
test('signing in opens the server from the account\'s list at web.getportico.tv; joining carries the code, and an HTTPS address, in the fragment', () => {
  assert.deepEqual(porticoHandoffUrl({hostedOrigin, serverId: 'srv_den', pageOrigin: 'https://a1.ns.portico.direct:32500'}),
    {url: 'https://web.getportico.tv/servers?open=srv_den', secure: true}, 'nothing of this page goes along when only signing in');
  assert.deepEqual(porticoHandoffUrl({hostedOrigin, serverId: 'srv_den', pageOrigin: 'https://a1.ns.portico.direct:32500', invitation: 'K7QP-2MXD-9HRT-4WNC'}),
    {url: 'https://web.getportico.tv/servers?open=srv_den#invitation=K7QP-2MXD-9HRT-4WNC&address=https%3A%2F%2Fa1.ns.portico.direct%3A32500', secure: true});
  assert.deepEqual(porticoHandoffUrl({hostedOrigin, serverId: 'srv_den', pageOrigin: 'http://192.168.1.20:32400', invitation: 'K7QP-2MXD-9HRT-4WNC'}),
    {url: 'https://web.getportico.tv/servers?open=srv_den#invitation=K7QP-2MXD-9HRT-4WNC', secure: false}, 'web.getportico.tv cannot reach a plain-HTTP address, so it is not sent');
});

function page(hash: string) {
  const stored = new Map<string, string>(), replaced: string[] = [];
  Object.assign(globalThis, {
    location: {hash, pathname: '/servers', search: '?open=srv_den'},
    history: {state: null, replaceState: (_: unknown, __: string, url: string) => {replaced.push(url); (globalThis as any).location.hash = '';}},
    sessionStorage: {getItem: (k: string) => stored.get(k) ?? null, setItem: (k: string, v: string) => {stored.set(k, v);}, removeItem: (k: string) => {stored.delete(k);}},
  });
  return {stored, replaced};
}
test('a handed invitation leaves the address bar at once, is kept in this tab through the sign-in, and is read once', async () => {
  const carry = await import('../src/platform/invitation-carry.ts');
  const start = {...await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {'../../platform/invitation-carry': carry}),
    takeHandoffInvitation: () => carry.takeCarriedInvitation()};
  const tab = page('#invitation=K7QP-2MXD-9HRT-4WNC&address=https%3A%2F%2Fa1.ns.portico.direct%3A32500');
  start.keepHandoffInvitation();
  assert.deepEqual(tab.replaced, ['/servers?open=srv_den'], 'the fragment is taken out of the address and its history entry');
  assert.deepEqual(start.takeHandoffInvitation(), {code: 'K7QP-2MXD-9HRT-4WNC', address: 'https://a1.ns.portico.direct:32500'});
  assert.equal(start.takeHandoffInvitation(), undefined, 'read once');
  // Only an HTTPS origin is filled in for the person; anything else is left for them to type.
  page('#invitation=K7QP-2MXD-9HRT-4WNC&address=http%3A%2F%2Fevil.example%2Fpath'); start.keepHandoffInvitation();
  assert.deepEqual(start.takeHandoffInvitation(), {code: 'K7QP-2MXD-9HRT-4WNC', address: ''});
  // Something that is not an invitation code is dropped, and an unrelated fragment is left alone.
  const bad = page('#invitation=not-a-code'); start.keepHandoffInvitation();
  assert.deepEqual(bad.replaced, ['/servers?open=srv_den']); assert.equal(start.takeHandoffInvitation(), undefined);
  const other = page('#join'); start.keepHandoffInvitation();
  assert.deepEqual(other.replaced, []); assert.equal(start.takeHandoffInvitation(), undefined);
});

test('an invitation carried for a sign-in that was left is not met by the next person in the tab', async () => {
  page('');
  const carry = await import('../src/platform/invitation-carry.ts');
  carry.carryInvitation('K7QP-2MXD-9HRT-4WNC', 'https://a1.ns.portico.direct:32500', 1_000_000);
  assert.equal(carry.takeCarriedInvitation(1_000_000 + 11 * 60_000), undefined, 'after ten minutes it is gone');
  carry.carryInvitation('K7QP-2MXD-9HRT-4WNC', '', 1_000_000); carry.forgetCarriedInvitation();
  assert.equal(carry.takeCarriedInvitation(1_000_000 + 1000), undefined, 'the account\'s sign-in ending forgets it');
});
