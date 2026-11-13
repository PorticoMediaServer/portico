// Forgot PIN's Google or Apple step on the web: a pop-up hands its return back to the page that opened
// it, for that flow only, on this origin only. Stand-in windows; nothing calls a provider or Services.
import test from 'node:test';
import assert from 'node:assert/strict';
import {openProviderPopup, returnToOpener} from '../src/platform/pin-provider-popup.ts';

const origin = 'https://web.getportico.tv';
function storage() {const map = new Map<string, string>(); return {getItem: (k: string) => map.get(k) ?? null, setItem: (k: string, v: string) => {map.set(k, v);}, removeItem: (k: string) => {map.delete(k);}, map};}
function opener(popup: unknown) {
  const listeners = new Set<(event: any) => void>(), session = storage();
  const page: any = {location: {origin}, sessionStorage: session, open: () => popup,
    addEventListener: (_: string, listener: any) => listeners.add(listener), removeEventListener: (_: string, listener: any) => listeners.delete(listener)};
  return {page, session, send: (event: any) => listeners.forEach(listener => listener(event))};
}
const back = (tx: string, from = origin) => `${from}/app/account-return#tx=${tx}&code=c0de`;

test('only a same-origin message from the pop-up this page opened, for this flow, completes it', async () => {
  const popup: any = {closed: false, close() {this.closed = true;}}, o = opener(popup);
  const answer = openProviderPopup('https://accounts.google.com/x', 'flow-1', o.page);
  assert.equal(o.session.map.get('portico.pinProviderFlow'), 'flow-1', 'the pop-up starts with this flow in its copy of the tab');
  o.send({origin: 'https://evil.example', source: popup, data: {kind: 'portico.pin-provider-return', href: back('flow-1')}});
  o.send({origin, source: {}, data: {kind: 'portico.pin-provider-return', href: back('flow-1')}});
  o.send({origin, source: popup, data: {kind: 'portico.pin-provider-return', href: back('flow-2')}});
  o.send({origin, source: popup, data: {kind: 'portico.pin-provider-return', href: back('flow-1', 'https://evil.example')}});
  o.send({origin, source: popup, data: {kind: 'portico.pin-provider-return', href: back('flow-1')}});
  assert.equal(await answer, back('flow-1'));
  assert.equal(o.session.map.has('portico.pinProviderFlow'), false, 'nothing kept afterwards');
});

test('a blocked pop-up and one closed early each refuse in their own words', async () => {
  await assert.rejects(openProviderPopup('https://accounts.google.com/x', 'flow-1', opener(null).page), {code: 'popup_blocked'});
  const popup: any = {closed: false}, closing = openProviderPopup('https://accounts.google.com/x', 'flow-1', opener(popup).page);
  popup.closed = true;
  await assert.rejects(closing, {code: 'provider_return_incomplete'});
});

test('the return page hands back only when this pop-up was opened for that flow by this origin', () => {
  const posted: unknown[][] = [];
  const page = (flow: string | undefined, openerOrigin = origin): any => {const s = storage(); if (flow) s.setItem('portico.pinProviderFlow', flow);
    return {location: {origin}, sessionStorage: s, opener: {location: {origin: openerOrigin}, postMessage: (...args: unknown[]) => posted.push(args)}};};
  assert.equal(returnToOpener('flow-1', 'c0de', page('flow-1')), true);
  assert.deepEqual(posted, [[{kind: 'portico.pin-provider-return', href: back('flow-1')}, origin]]);
  assert.equal(returnToOpener('flow-1', 'c0de', page('flow-2')), false, 'another flow');
  assert.equal(returnToOpener('flow-1', 'c0de', page(undefined)), false, 'no flow in this window');
  assert.equal(returnToOpener('flow-1', 'c0de', {...page('flow-1'), opener: null}), false, 'no opener: an app\'s browser');
  assert.equal(posted.length, 1);
});
