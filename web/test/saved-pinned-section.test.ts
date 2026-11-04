import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

(globalThis as {devicePixelRatio?: number}).devicePixelRatio = 1;
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SavedPhoneDirectory} = await import('../src/screens/saved/SavedPhoneDirectory.tsx');

const store = <T>(value: T) => ({subscribe: () => () => {}, getSnapshot: () => value});
const runtime = {summary: store({value: undefined}), posters: store({phase: 'idle'})} as never;
const pins = [{kind: 'collection', id: 'c1', name: 'Toy Story', pinRevision: 1}, {kind: 'playlist', id: 'p1', name: 'Road trip', pinRevision: 2}, {kind: 'view', id: 'v1', name: 'Unwatched 90s', pinRevision: 3}] as const;
const render = (props: Record<string, unknown>) => renderToStaticMarkup(createElement(SavedPhoneDirectory, {runtime, downloadsOffered: false, artwork: {foreground: () => undefined} as never, select() {}, openEntry() {}, ...props}));

test('the phone Saved directory draws "Pinned" with Edit pins, then each pin in the server\'s order with its kind (phone-app Saved)', () => {
  const html = render({pins: {list: pins, edit() {}}});
  assert.match(html, /<h2>Pinned<\/h2><button type="button" class="edit">Edit pins<\/button>/);
  const order = [...html.matchAll(/<span class="label">([^<]+)<small class="caption">([^<]+)<\/small>/g)].map(match => `${match[1]}/${match[2]}`);
  assert.deepEqual(order, ['Toy Story/Collection', 'Road trip/Playlist', 'Unwatched 90s/Saved view']);
});

test('with no pins the heading and Edit pins stay, so library pins can still be arranged; without pins bound nothing is drawn', () => {
  const empty = render({pins: {list: [], edit() {}}});
  assert.match(empty, /Edit pins/);
  assert.doesNotMatch(empty, /aria-label="Pinned"/);
  assert.doesNotMatch(render({}), /Edit pins/);
});

test('a pinned playlist opens its playlist; a collection or saved view opens its resource page, as native does', () => {
  const source = readFileSync(new URL('../src/screens/saved/SavedPhoneDirectory.tsx', import.meta.url), 'utf8');
  assert.match(source, /select\(pin\.kind === 'playlist' \? \{view: 'playlist', playlistId: pin\.id\} : \{view: 'resource', resourceId: pin\.id\}\)/);
});

test('Saved reads pins only on the phone directory and reads them again when the pin editor closes', () => {
  const route = readFileSync(new URL('../src/screens/saved/Saved.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /const pinsShown = useShellWidth\(\) < 600 && route\.view === 'home';/);
  assert.match(route, /const pinned = usePinnedResources\(context, pinsShown\), pinEditor = usePinEditor\(context, pinned\.reload\);/);
  assert.match(route, /pins=\{pinsShown \? \{list: pinned\.pins, edit: pinEditor\.open\} : undefined\}/);
  const editor = readFileSync(new URL('../src/screens/pins/PinEditor.tsx', import.meta.url), 'utf8');
  assert.match(editor, /onClose=\{\(\) => \{setOpen\(false\); onClosed\?\.\(\);\}\}/);
});

test('closing Edit pins reads the pins again after a read already out, so a pin removed meanwhile never comes back (Sol 8, #801)', async () => {
  const {PinsController} = await import('../src/screens/pins/pins-controller.ts');
  let order = [{kind: 'collection', id: 'c1', name: 'Toy Story', pinRevision: 1}];
  const held: (() => void)[] = [];
  let hold = true, gets = 0;
  const api = {request: async <T>(path: string): Promise<T> => {
    if (path === '/v1/me/library-navigation') return {pinnedLibraryIds: [], revision: 1} as T;
    gets++;
    // The answer is the order at the time the server read it, as a delayed response would be.
    const answer = {serverId: 'server', viewerFence: 'fence', revision: gets, order};
    if (hold) await new Promise<void>(resolve => held.push(resolve));
    return answer as T;
  }};
  const reader = new PinsController({api, serverId: 'server', current: () => true, libraryIds: () => [], requestId: async () => 'op'});
  const first = reader.load();
  await Promise.resolve();
  // The editor (its own controller) unpins c1 while Saved's first read is still out; then the editor closes.
  order = [];
  hold = false;
  const refreshed = reader.refresh();
  held.splice(0).forEach(resolve => resolve());
  await first; await refreshed;
  assert.equal(gets, 2, 'the close asks for its own read after the one already out');
  assert.deepEqual(reader.getSnapshot().resources?.order, [], 'the removed pin stays removed');
  // With nothing out, a refresh is one read.
  await reader.refresh();
  assert.equal(gets, 3);
  reader.dispose();
});

test('the Saved pin list re-reads through refresh, never by joining a read in flight', () => {
  assert.match(readFileSync(new URL('../src/screens/pins/usePinnedResources.ts', import.meta.url), 'utf8'), /reload: \(\) => \{void controller\?\.refresh\(\);\}/);
});
