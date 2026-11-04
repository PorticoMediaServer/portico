import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as serverLinks from '../../../packages/client-core/src/server-links.ts';

// N5-6: Share and Copy link speak for the server they're drawn on, as web's `shareableLink` does.
const shared: unknown[] = [], copied: string[] = [];
const {nativeShareLink, shareLinkActions} = await screenModule(new URL('../src-next/screens/share/share-actions.ts', import.meta.url), {
  react: {default: {}}, 'react-native': {Share: {share: async (value: unknown) => {shared.push(value);}}},
  '../../../../../packages/client-core/src/server-links': serverLinks,
  '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
  '../../platform/clipboard': {copyText: async (value: string) => {copied.push(value);}},
});

test('a Portico Account server shares the one canonical /s/ link', () => {
  const link = nativeShareLink({serverId: 'srv_1', hosted: true, origin: 'https://192.168.1.20:8443'}, 'item', {itemId: 'movie-1'});
  assert.equal(link, serverLinks.buildServerLink({serverId: 'srv_1', destination: 'item', params: {itemId: 'movie-1'}}));
  assert.ok(link!.startsWith('https://web.getportico.tv/s/srv_1/'));
});

test('a local-account server shares its own address, where its own web UI answers', () => {
  const own = serverLinks.destinationLocation('item', {itemId: 'movie-1'})!;
  assert.equal(nativeShareLink({serverId: 'srv_1', hosted: false, origin: 'https://den.example:8443/'}, 'item', {itemId: 'movie-1'}),
    `https://den.example:8443${own.pathname}${own.search}`);
  const library = serverLinks.destinationLocation('library', {libraryId: 'lib-2'})!;
  assert.equal(nativeShareLink({serverId: 'srv_1', hosted: false, origin: 'http://10.0.0.5:5191'}, 'library', {libraryId: 'lib-2'}),
    `http://10.0.0.5:5191${library.pathname}${library.search}`);
});

test('nothing is offered when no link can be built: no server, no address for a local server, or a refused destination', () => {
  assert.deepEqual(shareLinkActions(undefined, 'item', {itemId: 'movie-1'}), []);
  assert.deepEqual(shareLinkActions({serverId: 'srv_1', hosted: false}, 'item', {itemId: 'movie-1'}), []);
  assert.deepEqual(shareLinkActions({serverId: 'srv_1', hosted: false, origin: 'portico://x'}, 'item', {itemId: 'movie-1'}), []);
  assert.deepEqual(shareLinkActions({serverId: 'srv_1', hosted: false, origin: 'https://den.example'}, 'item', {itemId: '../x'}), []);
});

test('Share and Copy link hand over exactly that link', async () => {
  const actions = shareLinkActions({serverId: 'srv_1', hosted: false, origin: 'https://den.example'}, 'person', {personId: 'p1'});
  assert.deepEqual(actions.map((action: {id: string}) => action.id), ['share-link', 'copy-link']);
  const link = nativeShareLink({serverId: 'srv_1', hosted: false, origin: 'https://den.example'}, 'person', {personId: 'p1'});
  actions[0].onSelect(); actions[1].onSelect(); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(shared, [{message: link, url: link}]);
  assert.deepEqual(copied, [link]);
});
