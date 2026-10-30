import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {boundedPick, PICK_DEADLINE_MS} from '../src-next/platform/pick-deadline';
import {presentError} from '../../../packages/client-core/src/presentation/errors';

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('a pick step that never answers ends in a visible, retryable timeout naming the waiting step', async () => {
  const lines: string[] = [];
  let seen: AbortSignal | undefined;
  const failure = await boundedPick(async (signal, trace) => {
    seen = signal; trace.mark('select'); trace.mark('save-session');
    return new Promise<never>(() => {});
  }, new AbortController().signal, 20, line => lines.push(line)).then(() => undefined, error => error);
  assert.equal(failure?.code, 'timeout');
  assert.equal(failure?.step, 'save-session');
  assert.equal(seen?.aborted, true);
  assert.equal(lines.length, 1);
  const logged = JSON.parse(lines[0]!.replace(/^portico_profile_pick_timeout /, ''));
  assert.equal(logged.waiting, 'save-session');
  assert.deepEqual(logged.marks.map(([step]: [string]) => step), ['select', 'save-session']);
  const shown = presentError(failure, 'portico-account');
  assert.equal(shown.category, 'timeout');
  assert.equal(shown.silent, false);
  assert.ok(shown.body);
});

test('a pick that answers in time is untouched, and a cancelled sign-in aborts the pick', async () => {
  const lines: string[] = [];
  assert.equal(await boundedPick(async () => 'home', new AbortController().signal, 1000, line => lines.push(line)), 'home');
  const parent = new AbortController();
  let child: AbortSignal | undefined;
  const pending = boundedPick(async signal => {child = signal; return new Promise<void>((_, reject) => signal.addEventListener('abort', () => reject(new Error('aborted'))));}, parent.signal, 1000, line => lines.push(line));
  parent.abort();
  await assert.rejects(pending, /aborted/);
  assert.equal(child?.aborted, true);
  assert.deepEqual(lines, []);
});

test('the profile pick runs under the deadline and nothing on it waits on the sign-in hint', () => {
  assert.ok(PICK_DEADLINE_MS <= 30000);
  const signIn = source('../src-next/platform/server-sign-in.ts');
  const choose = signIn.slice(signIn.indexOf('  chooseProfile('), signIn.indexOf('  createMemberMove('));
  assert.match(choose, /await boundedPick\(async \(signal, trace\) =>/);
  assert.match(choose, /selectDirectProfile\([^;]*, signal\);/s);
  assert.doesNotMatch(choose, /this\.controller\.signal\);\n\s+this\.issued/);
  assert.match(signIn, /if \(pin\) void rememberNativeSignInHint\(/);
  const accounts = source('../src-next/platform/accounts.ts');
  for (const step of ['identity', 'server-name', 'read-sessions', 'save-session']) assert.match(accounts, new RegExp(`trace\\?\\.mark\\('${step}'\\)`));
});
