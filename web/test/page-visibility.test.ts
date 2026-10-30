import test from 'node:test';
import assert from 'node:assert/strict';

class FakeDocument extends EventTarget {visibilityState: 'visible' | 'hidden' = 'visible';}

test('onChange reports every hide and show and unsubscribes symmetrically', async () => {
  const fake = new FakeDocument();
  (globalThis as {document?: unknown}).document = fake;
  try {
    const {documentVisibility} = await import('../src/platform/playback/page-visibility.ts');
    const seen: boolean[] = [], shown: number[] = [];
    const stop = documentVisibility.onChange!(() => seen.push(documentVisibility.hidden()));
    const stopVisible = documentVisibility.onVisible(() => shown.push(1));
    fake.visibilityState = 'hidden'; fake.dispatchEvent(new Event('visibilitychange'));
    fake.visibilityState = 'visible'; fake.dispatchEvent(new Event('visibilitychange'));
    assert.deepEqual(seen, [true, false]);
    assert.equal(shown.length, 1);
    stop(); stopVisible();
    fake.visibilityState = 'hidden'; fake.dispatchEvent(new Event('visibilitychange'));
    assert.deepEqual(seen, [true, false]);
  } finally {
    delete (globalThis as {document?: unknown}).document;
  }
});
