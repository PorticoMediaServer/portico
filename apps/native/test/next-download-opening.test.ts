import test from 'node:test';
import assert from 'node:assert/strict';
import {captureNativeDownloadPaintCurrent, createNativeDownloadOpening} from '../src-next/screens/downloads/download-opening';
import {askNativeDownload, closeNativeDownload, getNativeDownloadSnapshot, nativeDownloadRequestKey, subscribeNativeDownload} from '../src-next/screens/downloads/request';

function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'one', viewerFence: 'viewer'};
  let selected = {sessionFamilyId: 'family', tokenGeneration: 'one', authorizationHorizon: 'horizon', deviceId: 'device', installationId: 'installation',
    viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'Owner'}};
  let route = {route: {kind: 'home'}, revision: 1}, active = true;
  const navigated: unknown[] = [];
  const context = {api: {}, authority: () => authority, session: () => selected, current: () => active,
    navigation: {getSnapshot: () => route, open: (destination: unknown, original: unknown) => navigated.push([destination, original])}};
  askNativeDownload({title: 'Film', itemId: 'item'});
  const request = getNativeDownloadSnapshot()!;
  return {context: context as never, request, navigated, authority, opening: createNativeDownloadOpening(context as never, request),
    contextCopy: () => ({...context}) as never,
    route: () => {route = {...route, revision: route.revision + 1};},
    family: () => {selected = {...selected, sessionFamilyId: 'successor'};},
    generation: () => {selected = {...selected, tokenGeneration: 'two'};},
    retire: () => {active = false;}};
}

test('equal-valued requests have different presentation identity and an old close cannot dismiss the successor', () => {
  const f = fixture(), oldKey = nativeDownloadRequestKey(f.request);
  askNativeDownload({title: 'Film', itemId: 'item'});
  const successor = getNativeDownloadSnapshot()!;
  assert.notEqual(nativeDownloadRequestKey(successor), oldKey);
  assert.equal(f.opening.current(), false);
  assert.equal(f.opening.close(), false);
  assert.equal(getNativeDownloadSnapshot(), successor);
  closeNativeDownload(successor);
});

test('ordinary close aborts business work but inert paint remains only on its original page', () => {
  const f = fixture(), ticket = f.opening.claim()!;
  assert.ok(ticket);
  assert.equal(f.opening.close(), true);
  assert.equal(f.opening.signal.aborted, true);
  assert.equal(f.opening.owns(ticket), false);
  assert.equal(f.opening.claim(), undefined);
  assert.equal(f.opening.paintCurrent(), true);
  f.route();
  assert.equal(f.opening.paintCurrent(), false);
});

test('route occurrence, family, token generation and bootstrap retirement cannot lend a stale opening authority', () => {
  for (const change of ['route', 'family', 'generation', 'retire'] as const) {
    const f = fixture(), ticket = f.opening.claim()!;
    f[change]();
    assert.equal(f.opening.current(), false, change);
    assert.equal(f.opening.owns(ticket), false, change);
    assert.equal(f.opening.paintCurrent(), false, change);
    closeNativeDownload(f.request);
  }
});

test('the page lifetime can revoke this request even while the shell stays current', () => {
  const f = fixture(), life = new AbortController();
  let page = true;
  askNativeDownload({title: 'Film', itemId: 'item', lifetime: {signal: life.signal, isCurrent: () => page}});
  const request = getNativeDownloadSnapshot()!, opening = createNativeDownloadOpening(f.context, request);
  assert.equal(opening.current(), true);
  page = false;
  assert.equal(opening.current(), false);
  page = true; life.abort();
  assert.equal(opening.current(), false);
  assert.equal(opening.claim(), undefined);
  closeNativeDownload(request);
});

test('the mutation claim exists before a supplied currentness callback can reenter it', () => {
  const f = fixture(), life = new AbortController();
  let opening: ReturnType<typeof createNativeDownloadOpening> | undefined, reenter = false;
  let nested: object | undefined;
  askNativeDownload({title: 'Film', itemId: 'item', lifetime: {signal: life.signal, isCurrent: () => {
    if (reenter) {reenter = false; nested = opening!.claim();}
    return true;
  }}});
  const request = getNativeDownloadSnapshot()!;
  opening = createNativeDownloadOpening(f.context, request); reenter = true;
  const ticket = opening.claim();
  assert.ok(ticket); assert.equal(nested, undefined);
  assert.equal(opening.claim(), undefined);
  opening.release({}); assert.equal(opening.claim(), undefined, 'foreign finally cannot release the owner');
  opening.release(ticket); assert.ok(opening.claim());
  closeNativeDownload(request);
});

test('replacing the request inside currentness refuses the pending old claim', () => {
  const f = fixture(), life = new AbortController(); let replace = false;
  askNativeDownload({title: 'Film', itemId: 'item', lifetime: {signal: life.signal, isCurrent: () => {
    if (replace) {replace = false; askNativeDownload({title: 'Film', itemId: 'item'});}
    return true;
  }}});
  const request = getNativeDownloadSnapshot()!, opening = createNativeDownloadOpening(f.context, request);
  replace = true;
  assert.equal(opening.claim(), undefined);
  assert.equal(opening.close(), false);
  assert.notEqual(getNativeDownloadSnapshot(), request);
  closeNativeDownload();
});

test('a late result cannot acquire a successor request or release its command', async () => {
  const f = fixture(), ticket = f.opening.claim()!;
  let finish!: () => void, published = false;
  const pending = new Promise<void>(resolve => {finish = resolve;}).then(() => {
    if (f.opening.owns(ticket)) published = true;
    f.opening.release(ticket);
  });
  askNativeDownload({title: 'Film', itemId: 'item'});
  const successor = createNativeDownloadOpening(f.context, getNativeDownloadSnapshot()!), next = successor.claim()!;
  finish(); await pending;
  assert.equal(published, false);
  assert.equal(successor.owns(next), true);
  closeNativeDownload();
});

test('View downloads uses captured authority and refuses replacement during compare-close publication', () => {
  const f = fixture();
  const off = subscribeNativeDownload(() => {
    if (!getNativeDownloadSnapshot()) askNativeDownload({title: 'Successor', itemId: 'item'});
  });
  try {assert.equal(f.opening.viewDownloads(), false); assert.deepEqual(f.navigated, []);}
  finally {off(); closeNativeDownload();}
  const next = fixture();
  assert.equal(next.opening.viewDownloads(), true);
  assert.deepEqual(next.navigated, [[{kind: 'downloads'}, next.authority]]);
});

test('View downloads never borrows a changed authority after close publishes', () => {
  const f = fixture();
  const off = subscribeNativeDownload(() => {f.authority.epoch = 'successor';});
  try {assert.equal(f.opening.viewDownloads(), false); assert.deepEqual(f.navigated, []);}
  finally {off(); closeNativeDownload();}
});

test('the store freezes target and lifetime descriptors without changing the offered signal', () => {
  const life = new AbortController(), container = {kind: 'season' as const, id: 'original'};
  const lifetime = {signal: life.signal, isCurrent: () => true};
  askNativeDownload({title: 'Series', container, lifetime});
  const request = getNativeDownloadSnapshot()!;
  container.id = 'changed'; lifetime.isCurrent = () => false;
  assert.equal(request.container?.id, 'original');
  assert.equal(request.lifetime?.signal, life.signal);
  assert.equal(request.lifetime?.isCurrent(), true);
  assert.equal(Object.isFrozen(request.container), true);
  assert.equal(Object.isFrozen(request.lifetime), true);
  closeNativeDownload(request);
});

test('retiring without close never rebinds the same resident request to a successor constructor', () => {
  for (const changed of ['route', 'family', 'generation', 'context'] as const) {
    const f = fixture();
    f.opening.retire();
    assert.equal(getNativeDownloadSnapshot(), f.request, 'layout cleanup left the request resident');
    if (changed !== 'context') f[changed]();
    const remount = createNativeDownloadOpening(changed === 'context' ? f.contextCopy() : f.context, f.request);
    let io = 0;
    if (remount.current() && remount.claim()) io++;
    assert.equal(remount.current(), false, changed);
    assert.equal(remount.claim(), undefined, changed);
    assert.equal(io, 0, changed);
    closeNativeDownload(f.request);
  }
});

test('StrictMode setup-cleanup-setup may replay only the exact original tuple', () => {
  const f = fixture();
  f.opening.retire();
  const replay = createNativeDownloadOpening(f.context, f.request), ticket = replay.claim();
  assert.equal(replay.current(), true);
  assert.ok(ticket);
  f.opening.release({});
  assert.equal(replay.owns(ticket), true);
  const unrelated = createNativeDownloadOpening(f.contextCopy(), f.request);
  assert.equal(unrelated.current(), false);
  assert.equal(replay.current(), false, 'a refused successor binding cannot be resurrected');
  closeNativeDownload(f.request);
});

test('same-tuple replay cannot duplicate the original request command while its retired owner settles', () => {
  const f = fixture(), old = f.opening.claim()!;
  f.opening.retire();
  const replay = createNativeDownloadOpening(f.context, f.request);
  assert.equal(replay.current(), true);
  assert.equal(replay.claim(), undefined);
  f.opening.release(old);
  const next = replay.claim();
  assert.ok(next);
  f.opening.release(old);
  assert.equal(replay.owns(next), true);
  closeNativeDownload(f.request);
});

test('a refused resident request constructor never closes an equal-valued successor request', () => {
  const f = fixture(); f.opening.retire(); f.family();
  const refused = createNativeDownloadOpening(f.context, f.request);
  assert.equal(refused.current(), false);
  askNativeDownload({title: 'Film', itemId: 'item'});
  const successor = getNativeDownloadSnapshot()!;
  assert.equal(refused.close(), false);
  assert.equal(getNativeDownloadSnapshot(), successor);
  closeNativeDownload(successor);
});


test('a Reading paint permit keeps the original page after local disposal and request removal only', () => {
  const f = fixture();let page = true;
  const paint = captureNativeDownloadPaintCurrent(f.context, f.request, () => page);
  assert.equal(paint(), true);f.opening.close();assert.equal(f.opening.current(), false);
  assert.equal(getNativeDownloadSnapshot(), undefined);assert.equal(paint(), true);
  page = false;assert.equal(paint(), false);page = true;assert.equal(paint(), false, 'original page retirement is monotonic');
});

test('a Reading constructor cannot use the same resident request for successor context or API paint', () => {
  for (const change of ['context', 'api', 'route', 'family', 'generation'] as const) {
    const f = fixture();const paint = captureNativeDownloadPaintCurrent(f.context, f.request);f.opening.retire();
    let context = f.context;
    if (change === 'context') context = f.contextCopy();
    else if (change === 'api') (context as unknown as {api: object}).api = {};
    else f[change]();
    const successor = captureNativeDownloadPaintCurrent(context, f.request);let io = 0;
    if (successor()) io++;assert.equal(io, 0, change);assert.equal(successor(), false);assert.equal(paint(), false);
    assert.equal(getNativeDownloadSnapshot(), f.request);closeNativeDownload(f.request);
  }
});

test('a reentrant original page check cannot lend its paint permit to a changed family', () => {
  const f = fixture();const paint = captureNativeDownloadPaintCurrent(f.context, f.request, () => {f.family();return true;});
  assert.equal(paint(), false);closeNativeDownload(f.request);
});
