import assert from 'node:assert/strict';
import test, {type TestContext} from 'node:test';
import {observeServerTime, resetServerClocks, serverNow} from '@core/server-clock.ts';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {AccountSecurityJournal} from '../src/platform/account-security-journal.ts';
import {captureLocalDeviceOpening} from '../src/screens/device/local-device-opening.ts';
import {createWebPreparingStarts} from '../src/platform/playback/start-owner.ts';
import {currentHomeLive} from '../src/screens/home/model.ts';
import {playlistRemovalUndo} from '../src/screens/saved/removal.ts';
import {restoreHostedOwnerPending, hostedApprovalPurpose} from '../src/services/owner-pending.ts';
import {captureHostedOwnerClaim} from '../src/services/owner-claim.ts';
import {hostedFixture, clone} from './support/account-security-journal-fixture.ts';

// No client-clock requirements: a device a year ahead or behind still works, because a server-issued
// instant is judged on the server's clock (learned from its own answers), never on this device's.
const YEAR = 365 * 24 * 3_600_000;
const origin = 'https://accounts.example';

/** The device clock reads `skew` away from the real time, and a server answer has taught the difference. */
function skewed(t: TestContext, skew: number) {
  const real = Math.floor(Date.now() / 1000) * 1000, device = real + skew;
  t.mock.method(Date, 'now', () => device);
  resetServerClocks();
  observeServerTime(origin, new Date(real).toUTCString(), device, device);
  t.after(resetServerClocks);
  return real;
}
const iso = (at: number) => new Date(at).toISOString().replace(/\.\d{3}Z$/, 'Z');

for (const [name, skew] of [['a year ahead', YEAR], ['a year behind', -YEAR]] as const) {
  test(`${name}: the account-security journal still opens an unexpired original`, async t => {
    const f = hostedFixture();
    skewed(t, skew);
    const journal = new AccountSecurityJournal({read: async () => clone(f.pending), change: async () => {}, assertAvailable: () => {}});
    assert.ok(await journal.read());
  });

  test(`${name}: a claim capture restores and its lookup is not refused`, async t => {
    const real = skewed(t, skew);
    const code = 'claim_' + Buffer.alloc(32, 5).toString('base64url');
    const href = origin + '/claim#' + new URLSearchParams({code, operation: 'current_operation', server: 'Actual server'});
    const page = {href, pathname: '/claim', search: '', hash: new URL(href).hash};
    const claim = captureHostedOwnerClaim(page, {state: null, replaceState: () => {}}, origin)!;
    let saved: unknown;
    const store = {assertAvailable: () => {}, read: async () => saved, change: async (update: (value: unknown) => unknown) => {saved = update(saved);}} as never;
    const pending = await restoreHostedOwnerPending(store, origin, claim);
    assert.ok(pending && pending.deadline > serverNow(), 'the local budget is the server clock plus the budget');
    assert.ok(await restoreHostedOwnerPending(store, origin), 'a reload inside the budget restores it');
    const account = {api: {origin, send: async () => ({code, serverId: 'srv_' + Buffer.alloc(32, 1).toString('base64url'), name: 'Actual server',
      address: 'https://original.example', purpose: 'switch', requestedAt: iso(real - 1000), expiresAt: iso(real + 90_000), status: 'pending', ownerTransactionId: 'opaque_transaction'})},
      service: {synchronize: async () => {}, accessSession: async () => ({account: {id: 'a'}, familyId: 'f', accessToken: 'x'}), getSnapshot: () => ({session: {account: {id: 'a'}, familyId: 'f', accessToken: 'x'}})}} as never;
    assert.equal(await hostedApprovalPurpose(account, store, pending!, new AbortController().signal), 'switch');
  });

  test(`${name}: Home live facts and a playlist undo window read on the server's clock`, t => {
    const real = skewed(t, skew);
    const live = {observedAt: iso(real - 60_000), expiresAt: iso(real + 60_000)} as never;
    assert.ok(currentHomeLive(live, serverNow()));
    assert.equal(currentHomeLive({observedAt: iso(real - 120_000), expiresAt: iso(real - 60_000)} as never, serverNow()), undefined);
    const receipt = {deleted: false, serverId: 's', viewerFence: 'v', playlistId: 'p', entryId: 'e', undoToken: 'u', undoUntil: iso(real + 30_000)} as never;
    assert.ok(playlistRemovalUndo(receipt, {serverId: 's', viewerFence: 'v', playlistId: 'p', entryId: 'e'}, 'Title'));
  });

  test(`${name}: an approval opening and a preparing-start owner stay valid until the server's horizon`, t => {
    const real = skewed(t, skew);
    const session = {accessToken: 'tok', tokenGeneration: '1', expiresAt: iso(real + 600_000), authorizationHorizon: iso(real + 3_600_000), sessionFamilyId: 'family', deviceId: 'device', installationId: 'install',
      viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'owner'}} as unknown as LocalSession;
    const location = {pathname: '/device', href: '/device', state: {__TSR_key: 'entry'}};
    const connection = {pin: {serverId: 'server', publicKey: 'key', fingerprint: 'fp'}, logicalOrigin: 'https://server.example', getSnapshot: () => ({phase: 'ready', origin: 'https://server.example'})};
    const api = {baseUrl: 'https://server.example', getRouteConnection: () => connection, request: async () => ({})} as unknown as HttpLocalApi;
    const opening = captureLocalDeviceOpening({pageOrigin: 'https://server.example', api, session: () => session, isCurrent: () => true, location, getLocation: () => location});
    assert.ok(opening && opening.current());
    opening.dispose();
    assert.ok(createWebPreparingStarts(api, session, () => session, () => true), 'the owner is created on a horizon the server has not reached');
  });
}
