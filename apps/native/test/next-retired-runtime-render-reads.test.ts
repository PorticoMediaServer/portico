import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import {createNativeStartupRecovery} from '../src-next/shell/startup-recovery.ts';

// E-tv-21: a retired runtime used to dispose itself and throw `native_shell_cancelled` from the
// first render read, which only AppErrorBoundary caught. Render reads now return the last values.
test('render reads of a retired runtime return its last values, retire it outside render, and never throw', async () => {
  const app = await standardHomeApp({mountUI: false});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {beginNativeInstallation} = await import('../src-next/platform/installation');
  const owner = new AbortController();
  const runtime = await restoreNativeShellRuntime(context => createNativeShellBindings(context), {signal: owner.signal, onAuthorityEnded() {}});
  try {
    assert.ok(runtime);
    const shown = runtime.getSnapshot(), motion = runtime.appearanceMotion, account = (runtime as unknown as {account: unknown}).account;
    // A newer installation generation retires this runtime without anyone telling it.
    const newer = beginNativeInstallation(new AbortController().signal);
    try {
      assert.equal(runtime.retirementSignal?.aborted, false, 'nothing disposes inside the render read');
      assert.equal(runtime.getSnapshot(), shown);
      assert.equal(runtime.appearanceMotion, motion);
      assert.equal((runtime as unknown as {account: unknown}).account, account);
      assert.equal(typeof runtime.skipBackSeconds, 'number');
      const unsubscribe = runtime.subscribe(() => {}); unsubscribe();
      await Promise.resolve(); await Promise.resolve();
      assert.equal(runtime.retirementSignal?.aborted, true, 'the retirement is published right after');
      assert.equal(runtime.getSnapshot(), shown, 'and the last snapshot stays readable until the tree unmounts');
    } finally {newer.dispose();}
  } finally {try {runtime?.dispose(); owner.abort();} finally {try {await app.unmount();} finally {app.disposeLoader();}}}
});

test('finishing recovery after a successful restore never aborts the signal the runtime lives under', async () => {
  let restoredUnder: AbortSignal | undefined;
  const recovery = createNativeStartupRecovery({startedAt: 0, current: () => true, foreground: true, online: true,
    restore: async signal => {restoredUnder = signal; return 'runtime';}, discard() {}, retryable: () => false,
    onReady() {}, onError() {}, onWait() {}, schedule: () => () => {}});
  for (let pass = 0; pass < 4; pass++) await Promise.resolve();
  assert.equal(restoredUnder?.aborted, false);
  recovery.finish(); recovery.dispose();
  assert.equal(restoredUnder?.aborted, false, 'the owner retires its runtime explicitly');
});

test('RestoredShell unmounts a runtime that retires, unless a sign-out, renewal or fence already moved on', () => {
  const shell = readFileSync(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), 'utf8');
  assert.match(shell, /runtime\?\.retirementSignal\?\.addEventListener\('abort', \(\) => queueMicrotask\(\(\) => \{\n\s+if \(!rootCurrent\(\) \|\| owned !== runtime\) return;/);
  assert.match(shell, /owned = undefined; publishState\(\{phase: 'loading'\}\); restart\(\);/);
});
