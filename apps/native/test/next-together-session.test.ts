import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src';
import {nativeTogetherSession} from '../src-next/screens/settings-live/together-session';
import type {ShellAuthority} from '../src-next/shell/model';

test('Together borrows one actual service per authority and closes its facts on retirement', async () => {
  const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home'};
  let current = true, requests = 0;
  const listeners = new Set<() => void>();
  const api = new HttpLocalApi('https://together-session.test', 'synthetic_access', async () => {
    requests++;
    return new Response(JSON.stringify({protocolVersion: '1.0', serverTime: new Date().toISOString(), groups: []}), {status: 200, headers: {'Content-Type': 'application/json'}});
  });
  const owner = {api, authority: () => authority, current: () => current, session: () => undefined,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const service = nativeTogetherSession(owner);
  try {
    assert.strictEqual(nativeTogetherSession(owner), service, 'route and player borrowers share the same real service');
    await service.refreshDirectory();
    assert.equal(requests, 1);
    assert.equal(service.getSnapshot().directoryPhase, 'ready');
    current = false; for (const listener of [...listeners]) listener();
    assert.equal(service.getSnapshot().directoryPhase, 'idle', 'a retained service exposes no old ready body after retirement');
    assert.equal(listeners.size, 0);
    assert.throws(() => nativeTogetherSession(owner), {name: 'AbortError'});
    await service.refreshDirectory();
    assert.equal(requests, 1, 'a retired borrower cannot dispatch another authenticated read');
  } finally {current = false; for (const listener of [...listeners]) listener(); service.dispose();}
});
