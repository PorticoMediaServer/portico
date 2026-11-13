import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {OwnerRemotePage, OwnAddressControl, OwnerRefusedAddresses, OwnerBlockedNetworks} from '../src/screens/settings/server-remote/OwnerRemotePage.tsx';

const status = {thisDevice: {atHome: true, address: '10.0.0.3'}, remoteSecurity: {active: 'standard'}, warnings: [], addresses: [],
 router: {state: 'open', protocol: 'UPnP'}, routerIPv6: {state: 'off'}, certificate: {state: 'ok', names: ['media.example']}, identityCode: 'actual-identity',
 refused: [{address: 'media.example:443', reason: 'address', fixes: ['allow'], refused: 200}],
 managedCertificate: {state: 'ready', ready: true, name: 'server.portico.example'}};
function source(outside?: unknown) {
 const snapshot = {busy: false, blocks: [{id: 'b', network: '10.2.3.4', blockedUntil: '2026-11-13T18:00:00Z'}], status: {...status, outside},
  interfaces: {document: {ownAddress: {enabled: true, domain: 'media.example', certificateFile: 'cert.pem', keyFile: 'key.pem'}}}};
 return {subscribe: () => () => {}, getSnapshot: () => snapshot, check: () => {throw Error('an outside check ran while rendering');}} as any;
}

test('Remote shows checked outside facts separately from an open router, with real certificate and blocked-address controls', () => {
 const state = source();
 const page = renderToStaticMarkup(createElement(OwnerRemotePage, {source: state, groups: 'rows', advanced: 'advanced rows', onConfirmed() {}}));
 assert.match(page, /Not checked from outside/);assert.match(page, /Port opened/);assert.doesNotMatch(page, /Reachable from outside/);
 assert.match(page, /server.portico.example/);
 const checked = renderToStaticMarkup(createElement(OwnerRemotePage, {source: source({checkedAt: '2026-11-13T18:00:00Z', addresses: [{address: 'media.example', port: 443, reachable: false}]}), groups: null, advanced: null, onConfirmed() {}}));
 assert.match(checked, /Not reachable from outside/);
 const own = renderToStaticMarkup(createElement(OwnAddressControl, {source: state}));
 assert.match(own, /cert.pem/);assert.match(own, /key.pem/);assert.match(own, /actual-identity/);
 const refused = renderToStaticMarkup(createElement(OwnerRefusedAddresses, {source: state, onStandard() {}, onCertificate() {}}));
 assert.match(refused, /media.example:443/);assert.match(refused, /Allow this address/);assert.doesNotMatch(refused, /200/);
 const blocks = renderToStaticMarkup(createElement(OwnerBlockedNetworks, {source: state}));
 assert.match(blocks, /10.2.3.4/);assert.match(blocks, /Unblock/);
});
