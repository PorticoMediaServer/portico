import assert from 'node:assert/strict';
import test from 'node:test';

// The shared react-native stub, made controllable for this file only.
const listeners: ((event: {url: string}) => void)[] = [];
let initialUrl: string | null = null;
const native = await import('react-native') as unknown as {Linking: Record<string, unknown>; Platform: {isTV?: boolean}};
native.Linking.addEventListener = (_name: string, listener: (event: {url: string}) => void) => {listeners.push(listener); return {remove() {listeners.splice(listeners.indexOf(listener), 1);}};};
native.Linking.getInitialURL = () => Promise.resolve(initialUrl);
const settle = () => new Promise(resolve => setTimeout(resolve, 0));

test('cold start: the launch link is pending until its consumer takes it; warm links replace it', async () => {
  initialUrl = 'portico://shared/srv_1/item/cold';
  const {nativeShareLinks} = await import('../src-next/platform/share-links.ts');
  const release = nativeShareLinks.acquire();
  await settle();
  const cold = nativeShareLinks.getSnapshot();
  assert.equal(cold?.webUrl, 'https://web.getportico.tv/s/srv_1/item/cold');
  assert.equal(cold?.warm, false);
  // Warm open while the app is running.
  for (const listener of [...listeners]) listener({url: 'portico://shared/srv_2/person/p1'});
  const warm = nativeShareLinks.getSnapshot();
  assert.equal(warm?.webUrl, 'https://web.getportico.tv/s/srv_2/person/p1');
  assert.equal(warm?.warm, true);
  // An older consumer can never clear the newer link.
  assert.equal(nativeShareLinks.take(cold!), false);
  assert.equal(nativeShareLinks.getSnapshot(), warm);
  assert.equal(nativeShareLinks.take(warm!), true);
  assert.equal(nativeShareLinks.getSnapshot(), undefined);
  // Other app routes are not shares.
  for (const listener of [...listeners]) listener({url: 'portico://item/abc'});
  assert.equal(nativeShareLinks.getSnapshot(), undefined);
  release();
  assert.equal(listeners.length, 0);
});
