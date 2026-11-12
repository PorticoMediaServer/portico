import test from 'node:test';
import assert from 'node:assert/strict';
import type {ProfileAPI} from '@core/profile-management.ts';
import {LocalOwnerTransferSource} from '../src/screens/settings/local-owner-transfer.ts';
import {guardedSettingsRequest, localOwnerTransferTransport} from '../src/screens/settings/settings-request.ts';

// ACC-LEAF-09: the original-opening boundaries of the Web new-owner variant,
// authored UNRUN. Fixtures carry actual People envelopes and revisions, and the
// transport is the production Settings/transfer boundary rather than a stand-in.

/** An account as the rebuilt server answers it. */
const member = (id: string, role = 'member') => ({id, username: id, role, disabled: false, createdAt: '2026-11-01T00:00:00Z', allLibraries: true, libraries: [], twoFactorEnabled: false});
function fixture() {
  let current = true, role = 'member', fail: unknown, complete = 0, reconciled = 0, navigated = 0;
  let post: (() => Promise<void>) | undefined;
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const api: ProfileAPI = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    if (path === '/v1/admin/playback/stream-limits' || path === '/v1/admin/sessions') throw Object.assign(new Error('not here'), {status: 404, code: 'not_found'});
    calls.push({path, method, body});
    if (path === '/v1/admin/members') return {items: [member('owner', 'owner'), member('member', role), member('admin', 'admin')]} as T;
    if (path === '/v1/admin/members/owner' || path === '/v1/admin/members/member') return (path.endsWith('/owner') ? member('owner', 'owner') : member('member', role)) as T;
    if (path === '/v1/admin/owner') {if (fail) throw fail; await post?.(); return undefined as T;}
    throw new Error('Unexpected transfer request');
  }};
  const build = new AbortController();
  const assertBuild = () => {if (!current) throw new DOMException('Settings build retired', 'AbortError');};
  const signal = (input?: AbortSignal | null) => input ? AbortSignal.any([build.signal, input]) : build.signal;
  const guarded: ProfileAPI = {request: guardedSettingsRequest(api, assertBuild, signal)};
  const source = new LocalOwnerTransferSource(guarded, {accountId: 'owner', profileId: 'owner-main', isCurrent: () => current,
    transferApi: localOwnerTransferTransport(api, assertBuild, signal),
    onTransferred: publish => {
      complete++;
      if (current) {reconciled++; if (publish) navigated++;}
      current = false;
    }});
  return {source, calls, get complete() {return complete;}, get reconciled() {return reconciled;}, get navigated() {return navigated;},
    retire: () => {current = false;build.abort();}, restoreParent: () => {current = true;},
    revised: () => {role = 'admin';}, fail: (error: unknown) => {fail = error;}, wait: (value: () => Promise<void>) => {post = value;}};
}
const caller = () => {
  const abort = new AbortController();
  return {signal: abort.signal, isCurrent: () => !abort.signal.aborted, retire: () => abort.abort()};
};
const newAccount = () => ({username: 'harbourview', name: 'Harbour View', password: 'New-password2!'});
const input = () => ({newAccount: newAccount(), currentPassword: ' original password ', expectedRevision: 1, previousOwner: 'admin' as const});
const ownership = (calls: {path: string; method?: string; body?: unknown}[]) => calls.filter(value => value.path === '/v1/admin/owner');
const drain = async (ready: () => unknown) => {for (let i = 0; i < 8 && !ready(); i++) await Promise.resolve();};

test('a caller-owned newAccount mutated after accept cannot change the accepting body', async () => {
  const f = fixture(), life = caller(), draft = newAccount(), command = {newAccount: draft, currentPassword: ' original password ', expectedRevision: 1, previousOwner: 'admin' as const};
  let release!: () => void;
  f.wait(() => new Promise<void>(resolve => {release = resolve;}));
  try {
    const port = await f.source.open(life);
    const pending = port.accept(command, life);
    draft.username = 'swapped'; draft.name = 'Swapped'; draft.password = 'Swapped-password9!';
    await drain(() => typeof release === 'function');
    release(); await pending;
    assert.deepEqual(ownership(f.calls).map(value => value.body), [{password: ' original password ',
      newAccount: {username: 'harbourview', password: 'New-password2!', profileName: 'Harbour View'}, previousOwner: 'admin'}]);
    assert.equal(f.complete, 1);
  } finally {f.source.dispose();}
});

test('a retained A new-owner port sends nothing after closing and reopening B', async () => {
  const f = fixture(), a = caller(), b = caller();
  try {
    const first = await f.source.open(a); first.close(); a.retire();
    const second = await f.source.open(b); first.close();
    const count = f.calls.length;
    await assert.rejects(first.accept(input(), a));
    assert.equal(f.calls.length, count);
    await second.accept(input(), b);
    assert.equal(f.complete, 1);
    assert.equal(ownership(f.calls).length, 1);
  } finally {f.source.dispose();}
});

test('an uncertain new-owner accept posts once, creates no member and reconciles no role', async () => {
  const f = fixture(), life = caller(), error = new Error('lost response'); f.fail(error);
  try {
    const port = await f.source.open(life);
    await assert.rejects(port.accept(input(), life), value => value === error);
    assert.equal(ownership(f.calls).length, 1);
    assert.equal(f.calls.some(value => value.method === 'POST' && /\/members(\?|$)/.test(value.path)), false);
    assert.equal(f.complete, 0); assert.equal(f.reconciled, 0);
    // Nothing retried on its own while the rejection settled.
    assert.equal(ownership(f.calls).length, 1);
  } finally {f.source.dispose();}
});

for (const retirement of ['command', 'authority'] as const) test('a known new-owner 204 after '+retirement+' retirement spends only the original context', async () => {
  const f = fixture(), life = caller(); let release!: () => void;
  f.wait(() => new Promise<void>(resolve => {release = resolve;}));
  try {
    const port = await f.source.open(life), pending = port.accept(input(), life);
    await drain(() => typeof release === 'function');
    if (retirement === 'command') life.retire(); else f.retire();
    release(); await pending;
    assert.equal(f.complete, 1);
    // A command-retired flight still owns publication; a retired authority cannot.
    assert.equal(f.reconciled, retirement === 'command' ? 1 : 0); assert.equal(f.navigated, 0);
    f.restoreParent();
    const after = f.calls.length;
    await assert.rejects(f.source.open(caller()));
    assert.equal(f.calls.length, after);
  } finally {f.source.dispose();}
});
