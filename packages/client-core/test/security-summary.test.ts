import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../i18n/src/index.ts';
import {parseSecuritySummary, securityBlockLine, securitySummaryLine, SecuritySummarySource} from '../src/server-admin/security-summary.ts';

const counts = (refused: number, failed: number, blocked: number) => ({refusedConnections: refused, refusedByReason: {name: refused}, failedSignIns: failed, blockedSources: blocked, newRemoteDevices: 0});
const reply = (blocks: unknown[] = []) => ({counts: {last24Hours: counts(3, 1, 0), last7Days: counts(212, 9, 1)}, blocks});
const block = {id: 'b1', network: '198.51.100.0/24', failedAttempts: 31, blockCount: 1, blockedUntil: '2026-11-01T16:15:00Z'};
const words = {t: defaultI18n.t as never, time: defaultI18n.time, dateTime: defaultI18n.dateTime};

test('the summary line is the last 7 days, with plurals, as drawn', () => {
  const summary = parseSecuritySummary(reply([block]))!;
  assert.equal(securitySummaryLine(summary, words), 'Last 7 days: 212 refused connections, 9 failed sign-ins, 1 blocked source');
  const one = parseSecuritySummary({counts: {last24Hours: counts(1, 1, 1), last7Days: counts(1, 1, 0)}, blocks: []})!;
  assert.equal(securitySummaryLine(one, words), 'Last 7 days: 1 refused connection, 1 failed sign-in, 0 blocked sources');
});

test('a blocked network says until when and how many failures', () => {
  const summary = parseSecuritySummary(reply([block]))!;
  const today = Date.parse('2026-11-01T12:00:00Z'), later = Date.parse('2026-10-30T12:00:00Z');
  assert.equal(securityBlockLine(summary.blocks[0]!, words, today), `Until ${defaultI18n.time(block.blockedUntil)}, 31 failed sign-ins`);
  assert.equal(securityBlockLine(summary.blocks[0]!, words, later), `Until ${defaultI18n.dateTime(block.blockedUntil)}, 31 failed sign-ins`);
});

test('an unreadable reply is refused and unreadable blocks are left out', () => {
  assert.equal(parseSecuritySummary({counts: {last24Hours: counts(1, 1, 1)}, blocks: []}), undefined);
  assert.equal(parseSecuritySummary({counts: {last24Hours: {...counts(1, 1, 1), refusedConnections: -1}, last7Days: counts(1, 1, 1)}, blocks: []}), undefined);
  const summary = parseSecuritySummary(reply([block, {id: '', network: 'x', failedAttempts: 1, blockedUntil: block.blockedUntil}, {...block, id: 'b2', blockedUntil: 'never'}]))!;
  assert.deepEqual(summary.blocks.map(item => item.id), ['b1']);
});

test('Unblock posts to the block and reads the summary again', async () => {
  const calls: string[] = [];
  let blocks: unknown[] = [block];
  const source = new SecuritySummarySource({request: async <T>(path: string, method: string) => {
    calls.push(`${method} ${path}`);
    if (method === 'POST') {blocks = [];return undefined as T;}
    return reply(blocks) as T;
  }}, () => true);
  await source.load();
  assert.equal(source.getSnapshot().summary?.blocks.length, 1);
  await source.unblock('b1');
  assert.deepEqual(calls, ['GET /v1/admin/security/summary', 'POST /v1/admin/security/blocks/b1/unblock', 'GET /v1/admin/security/summary']);
  assert.equal(source.getSnapshot().summary?.blocks.length, 0);
  assert.equal(source.getSnapshot().unblocked, 'b1');
});

test('a failed read says so and a retired page publishes nothing', async () => {
  let current = true;
  const source = new SecuritySummarySource({request: async () => {throw new Error('offline');}}, () => current);
  await source.load();
  assert.equal(source.getSnapshot().failed, true);
  current = false;
  const quiet = new SecuritySummarySource({request: async <T>() => reply() as T}, () => current);
  await quiet.load();
  assert.equal(quiet.getSnapshot().summary, undefined);
});
