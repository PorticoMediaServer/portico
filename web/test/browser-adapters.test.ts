import test from 'node:test';
import assert from 'node:assert/strict';
import {browserEventStream, browserFeedEnvironment} from '../src/platform/event-feed.ts';
import {bindBrowserFreshness, disposeFreshness, freshnessFor} from '../src/platform/freshness.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {webEnvironment} from '../src/platform/environment.ts';

function replace(t: import('node:test').TestContext, key: string, value: unknown) {
  const before = Object.getOwnPropertyDescriptor(globalThis, key);
  Object.defineProperty(globalThis, key, {configurable: true, value});
  t.after(() => { if (before) Object.defineProperty(globalThis, key, before); else delete (globalThis as Record<string, unknown>)[key]; });
}
test('event stream reads the current bearer for each opening, resumes and closes its channel', async t => {
  replace(t, 'window', {isSecureContext: true});
  replace(t, 'navigator', {locks: {request: async (_name: string, _options: unknown, work: () => unknown) => work()}});
  let channels = 0;
  class Channel { constructor(_name: string) { channels++; } postMessage() {} addEventListener() {} removeEventListener() {} close() { channels--; } }
  replace(t, 'BroadcastChannel', Channel);
  let token = 'synthetic-first'; const calls: {url: string; init: RequestInit}[] = [];
  const api = {baseUrl: 'https://server.example', routeFetch: async (url: string, init: RequestInit) => { calls.push({url, init}); return new Response('id: event-2\ndata: {"ok":true}\n\n', {headers: {'Content-Type': 'text/event-stream'}}); }} as unknown as HttpLocalApi;
  const open = browserEventStream(api, 'server/profile/family', () => token)!;
  for (const next of ['synthetic-first', 'synthetic-renewed']) {
    token = next;
    await assert.rejects(open('event-1', () => {}, new AbortController().signal), /event_stream_closed/);
    const last = calls.at(-1)!;
    assert.equal(last.url, 'https://server.example/v1/events');
    assert.equal((last.init.headers as Record<string, string>).Authorization, 'Bearer ' + next);
    assert.equal((last.init.headers as Record<string, string>)['Last-Event-ID'], 'event-1');
    assert.equal(last.init.cache, 'no-store'); assert.equal(channels, 0);
  }
});
test('event sharing falls back when broadcast/storage is unavailable; visibility listener detaches', t => {
  replace(t, 'window', {isSecureContext: false, get localStorage() { throw new Error('refused'); }});
  assert.equal(browserEventStream({} as HttpLocalApi, 'viewer', () => ''), undefined);
  const page = new EventTarget() as EventTarget & {visibilityState: string}; page.visibilityState = 'visible'; replace(t, 'document', page);
  const env = browserFeedEnvironment(() => false, () => () => {}); let heard = 0;
  const stop = env.onVisibility(() => heard++); page.dispatchEvent(new Event('visibilitychange')); stop(); page.dispatchEvent(new Event('visibilitychange'));
  assert.equal(heard, 1); page.visibilityState = 'hidden'; assert.equal(env.visible(), false); assert.equal(env.graceMs, 30000);
});
test('freshness pools are scoped to server/viewer and browser signals clean up', async () => {
  let follow: (() => void) | undefined, unsubscribed = false, phase = 'ready';
  const api = {getRouteSnapshot: () => ({phase}), subscribeRoute: (fn: () => void) => { follow = fn; return () => { unsubscribed = true; }; }} as unknown as HttpLocalApi;
  const one = freshnessFor(api, {serverId: 'server', viewerId: 'one'});
  assert.equal(freshnessFor(api, {serverId: 'server', viewerId: 'one'}), one);
  const page = new EventTarget() as EventTarget & {visibilityState: string}; page.visibilityState = 'visible'; const browser = new EventTarget();
  const stop = bindBrowserFreshness(one, null, page as Document, browser as Window);
  assert.equal(one.hub.isActive(), true); page.visibilityState = 'hidden'; page.dispatchEvent(new Event('visibilitychange')); assert.equal(one.hub.isActive(), false);
  phase = 'offline'; follow!(); stop(); assert.equal(unsubscribed, true);
  page.visibilityState = 'visible'; page.dispatchEvent(new Event('visibilitychange')); assert.equal(one.hub.isActive(), false, 'listener removed');
  const two = freshnessFor(api, {serverId: 'server', viewerId: 'two'}); assert.notEqual(one, two);
  await new Promise(resolve => setTimeout(resolve, 5)); assert.equal(one.hub.isActive(), false);
  assert.equal(one.queries.isDisposed(), true, 'the previous viewer\'s cache is emptied at once');
  disposeFreshness(two);
});
test('hosted flags retain explicit workspace and app version, and no build id is served', () => {
  assert.deepEqual(webEnvironment({VITE_HOSTED_WEB: 'true', VITE_ACCOUNT_WORKSPACE: 'true', VITE_HOSTED_URL: 'https://accounts.example', VITE_APP_VERSION: 'test', VITE_CAST_APPLICATION_ID: 'cast'}, {hostname: 'custom.example', origin: 'https://custom.example'}), {hostedWeb: true, accountWorkspace: true, hostedUrl: 'https://accounts.example', appVersion: 'test', castApplicationId: 'cast'});
});
