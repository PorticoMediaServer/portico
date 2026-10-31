import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {beginRestoreTrace, markRestore, restoreTrace} from '../src-next/shell/restore-trace.ts';

const source = (path: string) => readFileSync(new URL(`../src-next/${path}`, import.meta.url), 'utf8');

test('E-mobile-01: a finished pick always restarts the live root and logs the hand-off state', () => {
  const shell = source('shell/RestoredShell.tsx');
  const complete = shell.slice(shell.indexOf('  const complete = () => {'), shell.indexOf('  const retry = () =>'));
  assert.match(complete, /const live = root\.current;/);
  assert.match(complete, /portico_home_handoff/);
  assert.match(complete, /if \(live\?\.current\?\.\(\)\) \{live\.restart\?\.\(\); return;\}/);
  // A root whose installation claim went stale is retired and replaced, never left waiting.
  assert.match(complete, /live\?\.retire\?\.\(\); setAttempt\(value => value \+ 1\);/);
  assert.match(shell, /onAttemptTimeout: \(\) => \{try \{console\.warn\(`portico_restore_timeout/);
});

test('every restore step before Home is traced, and queued sign-outs wait until after it', () => {
  const bootstrap = source('shell/bootstrap.ts');
  // The connection itself is the session lifecycle's (C2b): one step here, its own reads in the ports.
  for (const step of ['installation', 'connection', 'mutations', 'first-reads', 'settings', 'bindings', 'publish'])
    assert.match(bootstrap, new RegExp(`markRestore\\('${step}'\\)`), step);
  // Queued sign-outs are the lifecycle's: they start after a delay, on the route it opened.
  const ports = source('platform/session-lifecycle-native.ts');
  assert.match(ports, /void wait\(deps\.drainDelayMs, /);
  assert.match(ports, /await connect\(serverId, signal\);\n\s+\/\/ .*\n\s+drainRevocations\(serverId\);/);
  assert.match(source('platform/installation-host.ts'), /if \(!valid && !invalidWarned && !disposed && !signal\?\.aborted\) \{/);
});

test('the trace names the step a restore is waiting on, with offsets only', () => {
  beginRestoreTrace(); markRestore('installation'); markRestore('connection');
  const trace = restoreTrace();
  assert.equal(trace.waiting, 'connection');
  assert.deepEqual(trace.marks.map(([step]) => step), ['installation', 'connection']);
  assert.deepEqual(Object.keys(trace).sort(), ['elapsedMs', 'marks', 'waiting']);
});
