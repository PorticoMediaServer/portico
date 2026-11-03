import assert from 'node:assert/strict';
import test from 'node:test';

// The shared react-native stub, made controllable for this file only.
const listeners: ((event: {url: string}) => void)[] = [];
let initialUrl: string | null = null;
const native = await import('react-native') as unknown as {Linking: Record<string, unknown>; Platform: {isTV?: boolean}};
native.Linking.addEventListener = (_name: string, listener: (event: {url: string}) => void) => {listeners.push(listener); return {remove() {listeners.splice(listeners.indexOf(listener), 1);}};};
native.Linking.getInitialURL = () => Promise.resolve(initialUrl);
const settle = () => new Promise(resolve => setTimeout(resolve, 0));
const {nativeContentLinks, parseContentLink} = await import('../src-next/platform/content-links.ts');
const {openContentLink} = await import('../src-next/screens/share/share-open.ts');

test('N5-1: the item and play links the OS hands over parse with the native allowlists\' grammar, and nothing else does', () => {
  assert.deepEqual(parseContentLink('portico://item/ep_12-a'), {action: 'item', itemId: 'ep_12-a'});
  assert.deepEqual(parseContentLink('portico://play/m1'), {action: 'play', itemId: 'm1'});
  for (const bad of ['portico://item/', 'portico://item/a/b', 'portico://play/a?x=1', 'portico://shared/srv/item/a', 'https://x/item/a', 'portico://item/' + 'a'.repeat(129), 42])
    assert.equal(parseContentLink(bad), undefined, String(bad));
});

test('a cold-launch content link waits for its consumer, on TV as on phones; a warm one replaces it', async () => {
  nativeContentLinks.resetForTest();
  const tv = native.Platform.isTV; native.Platform.isTV = true;
  initialUrl = 'portico://play/cold';
  const release = nativeContentLinks.acquire();
  await settle();
  const cold = nativeContentLinks.getSnapshot();
  assert.deepEqual({action: cold?.action, itemId: (cold as {itemId?: string} | undefined)?.itemId, warm: cold?.warm}, {action: 'play', itemId: 'cold', warm: false});
  for (const listener of [...listeners]) listener({url: 'portico://item/warm'});
  const warm = nativeContentLinks.getSnapshot();
  assert.equal((warm as {itemId?: string} | undefined)?.itemId, 'warm');
  assert.equal(nativeContentLinks.take(cold!), false, 'an older consumer never clears a newer link');
  assert.equal(nativeContentLinks.take(warm!), true);
  for (const listener of [...listeners]) listener({url: 'portico://shared/srv/item/x'});
  assert.equal(nativeContentLinks.getSnapshot(), undefined, 'share links are not content links');
  release(); assert.equal(listeners.length, 0);
  native.Platform.isTV = tv;
});

function deps(item: Record<string, unknown> | Error, online = true) {
  const log: unknown[][] = [];
  return {log, deps: {online, signal: new AbortController().signal,
    read: async (path: string) => {log.push(['read', path]); if (item instanceof Error) throw item; return item;},
    take: () => {log.push(['take']); return true;}, selectHome: () => {log.push(['home']);},
    open: (route: {kind: string}) => {log.push(['open', route.kind]);}, play: async (entry: {id: string; kind: string}) => {log.push(['play', entry.kind, entry.id]);}}};
}
const movie = {id: 'm1', kind: 'movie', title: 'Film', libraryId: 'lib', available: true, durationMs: 5_400_000, progressMs: 0};
const show = {id: 's1', kind: 'show', title: 'Show', libraryId: 'lib', available: true, durationMs: 0, progressMs: 0};

test('item opens the title over Home; play on a film or episode also resumes it; play on a show opens its page only', async () => {
  const item = deps(movie);
  assert.equal(await openContentLink({action: 'item', itemId: 'm1'}, item.deps as never), 'opened');
  assert.deepEqual(item.log, [['read', '/v1/items/m1'], ['take'], ['home'], ['open', 'title']]);
  const play = deps(movie);
  assert.equal(await openContentLink({action: 'play', itemId: 'm1'}, play.deps as never), 'played');
  assert.deepEqual(play.log.at(-1), ['play', 'movie', 'm1']);
  const series = deps(show);
  assert.equal(await openContentLink({action: 'play', itemId: 's1'}, series.deps as never), 'opened');
  assert.ok(!series.log.some(entry => entry[0] === 'play'));
});

test('a title this viewer cannot read says so (or that the server is offline), and opens nothing', async () => {
  const denied = deps(new Error('not_found'));
  assert.equal(await openContentLink({action: 'play', itemId: 'gone'}, denied.deps as never), 'no-access');
  assert.ok(!denied.log.some(entry => entry[0] === 'open' || entry[0] === 'play'));
  assert.equal(await openContentLink({action: 'item', itemId: 'gone'}, deps(new Error('network'), false).deps as never), 'offline');
});

test('N2-1: the account, Watch Together, downloads and old-invitation links the allowlists forward parse; /device is not one of them', () => {
  assert.deepEqual(parseContentLink('portico://account'), {action: 'account'});
  assert.deepEqual(parseContentLink('portico://account?tab=security'), {action: 'account'});
  assert.deepEqual(parseContentLink('portico://together/join/ab12-cd34'), {action: 'together', code: 'AB12-CD34'});
  assert.deepEqual(parseContentLink('portico://downloads'), {action: 'downloads'});
  assert.deepEqual(parseContentLink('portico://invite#invite=tok_en.1~'), {action: 'old-invite'});
  for (const bad of ['portico://device', 'portico://account?tab=billing', 'portico://account#x=1', 'portico://together/join/AB', 'portico://together/join/AB12?x=1',
    'portico://downloads?x=1', 'portico://invite#invite=a&b=c', 'portico://invite?invite=a', 'portico://join#code=x&server=y'])
    assert.equal(parseContentLink(bad), undefined, bad);
});

test('N2-1: an app link opens its page over Home: account is Settings › Profile, Together carries the code, Downloads only where there are downloads', async () => {
  const opened = async (link: Parameters<typeof openContentLink>[0], downloads = true) => {
    const log: unknown[][] = [];
    const outcome = await openContentLink(link, {online: true, signal: new AbortController().signal, downloads,
      read: async () => {throw new Error('no read');}, take: () => {log.push(['take']); return true;}, selectHome: () => {log.push(['home']);},
      open: route => {log.push(['open', route]);}, play: async () => {throw new Error('no play');}});
    return {outcome, log};
  };
  assert.deepEqual(await opened({action: 'account'}), {outcome: 'opened', log: [['take'], ['home'], ['open', {kind: 'shell', destination: {kind: 'settings', page: 'profile'}}]]});
  assert.deepEqual((await opened({action: 'together', code: 'AB12CD34'})).log.at(-1), ['open', {kind: 'together', joinCode: 'AB12CD34'}]);
  assert.deepEqual((await opened({action: 'downloads'})).log.at(-1), ['open', {kind: 'downloads'}]);
  assert.deepEqual(await opened({action: 'downloads'}, false), {outcome: 'opened', log: [['take'], ['home']]}, 'a TV lands on Home');
  assert.deepEqual(await opened({action: 'old-invite'}), {outcome: 'old-invite', log: [['take'], ['home']]}, 'the caller says to ask for a new one');
  const stale = await openContentLink({action: 'account'}, {online: true, signal: new AbortController().signal, downloads: true, read: async () => ({}), take: () => false,
    selectHome: () => {throw new Error('superseded links open nothing');}, open: () => {throw new Error('superseded links open nothing');}, play: async () => {}});
  assert.equal(stale, 'superseded');
});
