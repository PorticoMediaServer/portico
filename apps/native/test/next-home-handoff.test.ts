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
  for (const step of ['installation', 'sessions', 'selected', 'server-name', 'sign-in-hint', 'connection', 'viewer', 'mutations', 'first-reads', 'settings', 'bindings', 'publish'])
    assert.match(bootstrap, new RegExp(`markRestore\\('${step}'\\)`), step);
  assert.doesNotMatch(bootstrap, /if \(liveRoute\) void drainRevocations/);
  assert.match(bootstrap, /setTimeout\(\(\) => \{if \(!signal\.signal\.aborted\) void drainRevocations\(/);
  assert.match(source('platform/installation-host.ts'), /if \(!valid && !invalidWarned && !disposed && !signal\?\.aborted\) \{/);
});

test('the trace names the step a restore is waiting on, with offsets only', () => {
  beginRestoreTrace(); markRestore('installation'); markRestore('connection');
  const trace = restoreTrace();
  assert.equal(trace.waiting, 'connection');
  assert.deepEqual(trace.marks.map(([step]) => step), ['installation', 'connection']);
  assert.deepEqual(Object.keys(trace).sort(), ['elapsedMs', 'marks', 'waiting']);
});
