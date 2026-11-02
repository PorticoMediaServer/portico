import test from 'node:test';
import assert from 'node:assert/strict';
import {clockOrigin, observeResponseClock, observeServerTime, resetServerClocks, restoreServerClock, serverClockOffset, serverNow} from '../src/server-clock.ts';
import {parseLocalSession} from '../src/local-session.ts';

const YEAR = 365 * 86400000;
const server = 'http://192.168.1.20:32500';
/** Run with this device's wall clock moved by `offset`; the server keeps real time. */
async function withDeviceClock<T>(offset: number, run: (real: () => number) => T | Promise<T>): Promise<T> {
  const original = Date.now, real = () => original.call(Date);
  Date.now = () => real() + offset;
  try { return await run(real); } finally { Date.now = original; resetServerClocks(); }
}

test('an answer\'s Date teaches that origin\'s clock, taken at the middle of the exchange', async () => {
  await withDeviceClock(YEAR, real => {
    const sentAt = Date.now();
    observeResponseClock(clockOrigin(server + '/v1/me'), {headers: new Headers({Date: new Date(real()).toUTCString()})}, sentAt, sentAt + 200);
    // An HTTP date has one-second resolution; the offset is a year, give or take that.
    assert.ok(Math.abs(serverClockOffset(server) + YEAR) < 2000);
    assert.ok(Math.abs(serverNow(server) - real()) < 2000, 'the server\'s time, not this device\'s');
  });
});

test('an origin not yet heard from borrows the latest offset; nothing heard means none', async () => {
  await withDeviceClock(-YEAR, real => {
    assert.equal(serverClockOffset('https://other.example'), 0);
    observeServerTime(server, real(), Date.now(), Date.now());
    assert.ok(Math.abs(serverClockOffset('https://other.example') - YEAR) < 50);
  });
});

test('a header the browser hides teaches nothing', async () => {
  await withDeviceClock(YEAR, () => {
    observeResponseClock(server, {headers: new Headers()}, Date.now());
    observeServerTime(server, 'not a date', Date.now());
    assert.equal(serverClockOffset(server), 0);
  });
});

test('a saved offset judges stored instants before any server has answered, and stays saved', async () => {
  await withDeviceClock(YEAR, real => {
    const saved: number[] = [];
    restoreServerClock(-YEAR, offset => saved.push(offset));
    assert.ok(Math.abs(serverNow() - real()) < 50);
    observeServerTime(server, real(), Date.now(), Date.now());
    assert.equal(saved.length, 1);
  });
});

const fresh = (expiresAt: string) => ({accessToken: 'access', sessionFamilyId: 'family', tokenGeneration: '1', expiresAt, authorizationHorizon: '2099-02-01T00:00:00Z',
  viewer: {authority: 'local', accountId: 'account', profileId: 'profile', serverId: 'server', role: 'member'}});

test('No client-clock requirements: a device a year ahead accepts a sign-in the server just issued', async () => {
  await withDeviceClock(YEAR, real => {
    const expiresAt = new Date(real() + 15 * 60000).toISOString();
    observeServerTime(server, real(), Date.now(), Date.now());
    assert.equal(parseLocalSession(fresh(expiresAt)).expiresAt, expiresAt);
  });
});
