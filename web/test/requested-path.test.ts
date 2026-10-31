import test from 'node:test';
import assert from 'node:assert/strict';

// A browser tab's sessionStorage: one per tab, kept across reloads, never shared.
class TabStorage {values = new Map<string, string>(); getItem(k: string) {return this.values.get(k) ?? null;} setItem(k: string, v: string) {this.values.set(k, v);} removeItem(k: string) {this.values.delete(k);}}
const tab = new TabStorage();
(globalThis as {sessionStorage?: unknown}).sessionStorage = tab;
const {rememberRequestedPath, takeRequestedPath, REQUESTED_PATH_MAX_AGE_MS} = await import('../src/platform/requested-path.ts');

test('the page a tab asked for before sign-in or Who\'s watching opens once, afterwards', () => {
  tab.values.clear();
  rememberRequestedPath('/movies/m1?tab=cast#top', 1_000);
  assert.equal(takeRequestedPath(2_000), '/movies/m1?tab=cast#top');
  assert.equal(takeRequestedPath(3_000), undefined, 'used once');
});

test('entry pages, Home, developer pages and other origins are never remembered', () => {
  tab.values.clear();
  for (const href of ['/', '/sign-in', '/profiles?profileId=a', '/servers', '/setup', '/account', '/auth/portico-return?code=x', '/dev/kit', '//evil.example/x', 'https://evil.example/x'])
    rememberRequestedPath(href, 1_000);
  assert.equal(takeRequestedPath(2_000), undefined);
});

test('an abandoned attempt is forgotten: too old, from the future, or unreadable', () => {
  tab.values.clear();
  rememberRequestedPath('/shows/s1', 0);
  assert.equal(takeRequestedPath(REQUESTED_PATH_MAX_AGE_MS + 1), undefined);
  rememberRequestedPath('/shows/s1', 10 * 60_000);
  assert.equal(takeRequestedPath(0), undefined);
  tab.setItem('portico.requested-path.v1', '{not json');
  assert.equal(takeRequestedPath(0), undefined);
  assert.equal(tab.getItem('portico.requested-path.v1'), null, 'and cleared');
});

test('a later request replaces an earlier one', () => {
  tab.values.clear();
  rememberRequestedPath('/movies/m1', 1_000);
  rememberRequestedPath('/music/albums/a1', 2_000);
  assert.equal(takeRequestedPath(3_000), '/music/albums/a1');
});
