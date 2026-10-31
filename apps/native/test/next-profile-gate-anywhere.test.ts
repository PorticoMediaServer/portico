import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';

// Android walk, 31 Oct: a PIN changed elsewhere reached the PIN screen only from the shell's own
// reads; a screen's request (Saved) showed "This couldn't load". The shell now listens to the
// connection itself, so any request refused with profile_pin_reentry asks for the PIN.

test('the shell hears every profile_pin_reentry on its connection and ends through the PIN gate', () => {
  const source = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');
  const subscription = source.indexOf('local.subscribeProfileGate(');
  assert.ok(subscription > 0, 'subscribed on the shell connection');
  assert.match(source.slice(subscription - 200, subscription + 120), /cleanups\.push\(local\.subscribeProfileGate\(error => queueMicrotask\(\(\) => refused\(error\)\)\)\)/,
    'released with the shell, and handled outside the request that failed');
  assert.match(source, /const refused = \(cause\?: unknown\) => \{if \(nativeProfileGated\(cause\)\) end\(new NativeProfileGated\(\)\)/,
    'refused sends a gated refusal to the PIN screen, never the role probe');
  assert.ok(source.indexOf('const refused =') < subscription, 'defined before the subscription');
});
