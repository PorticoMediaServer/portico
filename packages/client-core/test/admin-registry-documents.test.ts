import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBackupsDocument, parseEnvelope} from '../src/administration.ts';

// NEW-36: registry (apikit) routes answer the bare document, with no administration envelope. The
// bodies below are what the server sends (GET /v1/admin/backups idle, running and listed).
test('a bare registry document is read as the result', () => {
  assert.deepEqual(parseEnvelope({backups: []}, 'srv', parseBackupsDocument).result.backups, []);
  const running = parseEnvelope({backups: [], running: {jobId: 'job', phase: 'snapshot', bytesDone: 0, bytesTotal: 3708486944}}, 'srv', parseBackupsDocument).result;
  assert.equal(running.running?.jobId, 'job');
  const listed = parseEnvelope({backups: [{id: '2026-09-24T061913Z', createdAt: '2026-09-24T06:19:13Z', kind: 'manual', schemaVersion: 220, serverVersion: '0.1.0-dev', bytes: 3704070907, path: '2026-09-24T061913Z'}]}, 'srv', parseBackupsDocument).result;
  assert.equal(listed.backups.length, 1);
  assert.equal(listed.backups[0]!.kind, 'manual');
});

test('an envelope still checks its server', () => {
  assert.equal(parseEnvelope({protocolVersion: '1.0', serverId: 'srv', result: {backups: []}}, 'srv', parseBackupsDocument).result.backups.length, 0);
  assert.throws(() => parseEnvelope({protocolVersion: '1.0', serverId: 'other', result: {backups: []}}, 'srv', parseBackupsDocument), /different server/);
  // Half an envelope is still refused as an envelope, never read as a document.
  assert.throws(() => parseEnvelope({serverId: 'srv', result: {backups: []}}, 'srv', parseBackupsDocument));
});
