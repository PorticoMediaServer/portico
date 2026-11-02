import test from 'node:test';
import assert from 'node:assert/strict';
import {DeviceAuthorizationService} from '../src/index.ts';
import {observeServerTime, resetServerClocks, serverNow} from '../src/server-clock.ts';
import {SetupCodeApprover, SetupCodeService, type SetupCodeJournal} from '../src/setup-code.ts';
import {TVConsentApprover, type TVConsentAuthority} from '../src/tv-consent.ts';
import {receiptUsable, topShelfExpired, type ReceiptClaims} from '../src/index.ts';
import {rememberedFor} from '../src/remember-marker.ts';
import {moveOfferDays} from '../src/sign-in.ts';
import {captureReportContext} from '../src/report-context.ts';
import {recordMembershipEndingShown, parseMembershipEndingNotice} from '../src/membership-ending.ts';
import {securityBlockLine} from '../src/server-admin/security-summary.ts';
import {parseRetryAfter} from '../src/timer.ts';
import {providerProblem} from '../src/subtitles.ts';

// No client-clock requirements (planning/DECISIONS.md): a device whose clock is a year ahead or behind still
// works. The "server" below keeps real time; the device clock is Date.now stubbed by +/- one year, and the
// server clock is taught the way a response's Date header teaches it.
const YEAR = 365 * 86_400_000;
const SKEWS = [['a year ahead', YEAR], ['a year behind', -YEAR]] as const;
const real = Date.now;
const iso = (ms: number) => new Date(ms).toISOString().replace(/\.\d+Z$/, 'Z');
function skewed(t: {after(fn: () => void): void}, skew: number, origin = 'https://server.test') {
  const truth = real();
  resetServerClocks();
  t.after(() => {Date.now = real; resetServerClocks();});
  Date.now = () => truth + skew;
  observeServerTime(origin, new Date(truth).toUTCString(), Date.now() - 20, Date.now() + 20);
  return truth; // the server's time
}
const settle = async () => {for (let n = 0; n < 32; n++) await Promise.resolve(); await new Promise(r => setTimeout(r, 30));};

for (const [label, skew] of SKEWS) {
  test(`device activation waits for a code that expires in the server's future when the clock is ${label}`, async t => {
    const truth = skewed(t, skew);
    const authorization = {deviceCode: 'private-secret', userCode: 'ABCD-EFGH', verificationUri: 'https://example.com/device', verificationUriComplete: 'https://example.com/device#code=ABCD-EFGH', expiresIn: 600, expiresAt: iso(truth + 600_000), interval: 0};
    let polls = 0;
    const service = new DeviceAuthorizationService({create: async () => authorization, poll: async () => {polls++; throw Object.assign(new Error('pending'), {status: 400, code: 'authorization_pending'});}, cancel: async () => {}, revoke: async () => {}}, {minimumPollMs: 1, requestTimeoutMs: 100}, async () => crypto.randomUUID());
    await service.start('TV', 'tvos');
    assert.equal(service.getSnapshot().phase, 'waiting');
    await settle();
    assert.notEqual(service.getSnapshot().phase, 'error');
    service.cancel();
  });

  test(`the setup code service shows a fresh code instead of refusing or replacing it when the clock is ${label}`, async t => {
    const truth = skewed(t, skew);
    let journal: SetupCodeJournal<string> | undefined, creates = 0;
    const storage = {change: async (update: (r: SetupCodeJournal<string> | undefined) => SetupCodeJournal<string>) => (journal = update(journal))};
    const api = {
      create: async () => {creates++; return {deviceCode: 'd'.repeat(43), userCode: 'ABCD-EFGH', verificationUri: 'https://example.com/device', verificationUriComplete: 'https://example.com/device#code=ABCD-EFGH', expiresIn: 600, expiresAt: iso(truth + 600_000), interval: 5};},
      poll: async () => {throw Object.assign(new Error('pending'), {status: 400, code: 'authorization_pending'});},
      cancel: async () => {},
    };
    const service = new SetupCodeService<string>('local:srv', api as never, storage, async () => crypto.randomUUID(), {requestTimeoutMs: 1000, tickMs: 60_000, replaceBeforeMs: 30_000});
    await service.start();
    await settle();
    assert.equal(service.getSnapshot().phase, 'waiting');
    assert.equal(creates, 1);
    await service.cancel();
  });

  test(`a reviewed setup code and a reviewed TV request are not refused for their expiry when the clock is ${label}`, async t => {
    const truth = skewed(t, skew);
    const preview = {requestId: 'request', userCode: '2345-ABCD', deviceName: 'Living room', platform: 'tv', appVersion: '1', expiresAt: iso(truth + 300_000)};
    const reviewed = await new SetupCodeApprover({baseUrl: 'https://server.test', request: async <T>() => preview as T} as never, 'local').review('2345ABCD');
    assert.equal(reviewed.requestId, 'request');
    const authority: TVConsentAuthority = {kind: 'local', origin: 'https://server.test', serverId: 'server', accountId: 'account', sessionFamilyId: 'family'};
    const calls: string[] = [];
    const approver = new TVConsentApprover({baseUrl: authority.origin, request: async <T>(path: string) => {calls.push(path); return (calls.length === 1 ? preview : undefined) as T;}}, authority, () => authority);
    const tv = await approver.review('2345ABCD');
    await approver.decide(tv, 'approve');
    assert.equal(calls.length, 2);
    approver.dispose();
  });

  test(`stored and displayed server instants read against the server's clock when the device clock is ${label}`, async t => {
    const truth = skewed(t, skew);
    // an offline receipt is judged by how long this device has held it, not by the date on the device
    const claims = {receiptId: 'r', viewer: {authority: 'local', accountId: 'a', profileId: 'p', serverId: 's'}, issuedAt: iso(truth), expiresAt: iso(truth + 30 * 86_400_000)} as unknown as ReceiptClaims;
    const viewer = claims.viewer, receivedAt = Date.now();
    assert.equal(receiptUsable(claims, viewer, new Date(receivedAt + 86_400_000), new Set(), receivedAt), true);
    assert.equal(receiptUsable(claims, viewer, new Date(receivedAt + 31 * 86_400_000), new Set(), receivedAt), false);
    // the remembered profile and the top shelf are the server's expiry against the server's now
    const held = {serverId: 's', familyId: 'f', profileId: 'p'};
    assert.equal(rememberedFor({...held, expiresAt: iso(truth + 60_000)}, held), true);
    assert.equal(rememberedFor({...held, expiresAt: iso(truth - 60_000)}, held), false);
    assert.equal(topShelfExpired({expiresAt: iso(truth + 60_000)} as never), false);
    assert.equal(topShelfExpired({expiresAt: iso(truth - 60_000)} as never), true);
    // a move offer counts its days from the server's calendar, and a report is captured for the server's now
    assert.equal(moveOfferDays(iso(truth + 3 * 86_400_000 + 3_600_000)) !== undefined, true);
    assert.ok(Math.abs(Date.parse(captureReportContext('UTC').until) - truth) <= 15 * 60_000);
    // an ending notice is recorded while the server says it is still ahead
    const notice = parseMembershipEndingNotice({serverId: 'srv_' + 'A'.repeat(43), membershipId: 'a'.repeat(32), sequence: 1, cause: 'owner_removed', expiresAt: iso(truth + 30 * 86_400_000)});
    const values = new Map<string, string>();
    assert.equal(recordMembershipEndingShown({getItem: k => values.get(k) ?? null, setItem: (k, v) => {values.set(k, v);}}, 'acct', notice), true);
    // a retry date is a wait measured on the server's clock
    assert.equal(parseRetryAfter(new Date(truth + 30_000).toUTCString()) !== undefined, true);
    assert.ok(parseRetryAfter(new Date(truth + 30_000).toUTCString())! <= 31_000);
    // "resets at" is the server's now plus its wait
    assert.ok(providerProblem({code: 'subtitle_provider_quota', retryAfterSeconds: 120})?.text);
    // a block that ends later today reads as a time of day on the server's calendar
    const words = {t: ((_id: string, values?: Record<string, string | number>) => String(values?.time)) as never, time: () => 'TIME', dateTime: () => 'DATETIME'};
    assert.equal(securityBlockLine({blockedUntil: iso(truth + 60_000), failedAttempts: 3} as never, words), new Date(truth + 60_000).getDate() === new Date(serverNow()).getDate() ? 'TIME' : 'DATETIME');
    assert.ok(Math.abs(serverNow() - truth) < 1000);
  });
}
