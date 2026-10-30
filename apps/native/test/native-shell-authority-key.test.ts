import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// H-11: the whole native shell is keyed on its authority. Only a real family, profile, PIN, privacy
// fence or sign-out change may remount it: never a token renewal, reconnect or route change.
test('H-11: the shell authority never carries the token horizon, so a renewal cannot remount the shell', () => {
  const bootstrap = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');
  const epoch = bootstrap.match(/epoch: JSON\.stringify\(\[([^\]]*)\]\), get viewerFence\(\) \{return viewerFence;\}/);
  assert.ok(epoch, 'the shell authority is built in one place');
  assert.equal(epoch![1], 'session.sessionFamilyId, profile.revision, profile.pinRevision');
  const renewal = bootstrap.slice(bootstrap.indexOf('if (renewed.authorizationHorizon !== session!.authorizationHorizon) {'), bootstrap.indexOf('// The refresher atomically owns'));
  assert.doesNotMatch(renewal, /onSessionRenewed/, 'a sliding horizon renews in place; it never restarts the app');
  const model = readFileSync(new URL('../src-next/shell/model.ts', import.meta.url), 'utf8');
  // The shell and playback keys agree, on the privacy fence: a library change keeps both.
  assert.match(model, /authorityKey = \(scope: ShellAuthority\): string => JSON\.stringify\(\[scope\.serverId, scope\.accountId, scope\.profileId, scope\.epoch, scope\.privacyFence \?\? scope\.viewerFence\]\)/);
  for (const file of ['runtime.ts', 'native.tsx', 'native.ios.tsx']) {
    const source = readFileSync(new URL('../src-next/platform/playback/' + file, import.meta.url), 'utf8');
    assert.match(source, /value\.epoch, value\.privacyFence \?\? value\.viewerFence\]/, file + ' keys playback on the privacy fence');
    assert.doesNotMatch(source, /epoch, [a-zA-Z.()]*viewerFence\]/, file + ' has no viewer-fence key left');
  }
});
