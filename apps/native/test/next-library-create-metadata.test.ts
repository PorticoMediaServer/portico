import assert from 'node:assert/strict';
import test from 'node:test';
import {LibraryManagementService} from '../../../packages/client-core/src/library-management.ts';
import {readOwnerKindMetadataAgents} from '../../../packages/client-core/src/owner-library-requests.ts';
import {parseKindMetadataAgents} from '../../../packages/client-core/src/server-admin/metadata-agent.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {all, createRenderer, tick, tree} from '../../../web/test/helpers/interaction-harness.mjs';

function fixture() {
  const h = createRenderer(), posts: any[] = [], reads: string[] = [];
  const local = {id: 'local', available: true};
  const metadataProviders = {
    movies: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, local],
    anime: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, {id: 'anilist', available: true}, local],
  };
  let created: any;
  const api = {request: async <T,>(path: string, method = 'GET', body?: any): Promise<T> => {
    const url = new URL(path, 'https://fixture.invalid');
    if (method === 'POST') {
      posts.push(body); created = {id: 'created', name: body.name, kind: body.kind, revision: '1', scanning: false,
        folders: body.folders.map((path: string) => ({path, available: true})), options: {depth: 'basic'}, metadataProvider: body.metadataProvider};
      return created as T;
    }
    reads.push(url.pathname);
    return (url.pathname === '/v1/admin/libraries' ? {items: created ? [created] : [], metadataProviders}
      : url.pathname === '/v1/admin/jobs' ? {items: []} : created) as T;
  }};
  const service = new LibraryManagementService({api, scope: {serverId: 'fixture', viewerId: 'owner'}, readRetries: 1});
  return {h, posts, reads, api, service};
}

test('phone first-library editor reads choices, changes kind and sends AniList before its first scan', async () => {
  const f = fixture(), core = '../../../../../../packages/client-core/src/';
  const app = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/AddLibraryPage.tsx', import.meta.url), {
    react: f.h.react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    [core + 'owner-library-requests']: {readOwnerKindMetadataAgents}, [core + 'server-admin/metadata-agent']: {parseKindMetadataAgents},
    [core + 'presentation/language-list']: {languageName: (code: string) => code},
    '../../../../../../packages/i18n/src/index': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/theme': {useTheme: () => ({colors: {}})}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Focusable': {Focusable: 'Focusable'},
    '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/controls/Select': {Select: 'Select'},
    '../../../kit/controls/FormError': {FormError: 'FormError'}, '../../../platform/keyboard': {FormScrollView: 'FormScrollView'},
    './controls': {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow', SectionTitle: 'SectionTitle'},
    './LibraryKindTiles': {LibraryKindTiles: 'LibraryKindTiles'}, './FolderSourcePicker': {FolderSourcePicker: 'FolderSourcePicker'},
    './presentation': {ownerLibraryKind: (kind: string) => ({label: kind})},
  });
  const onCreate = (input: any) => f.service.createLibrary(input), onCancel = () => {};
  const paint = () => tree(f.h.render(() => app.AddLibraryPage({api: f.api, serverName: 'Fixture', onCreate, onCancel, busy: false, firstRun: true})));
  try {
    paint(); await tick(); let view = paint();
    assert.ok(all(view, 'Focusable').some((node: any) => node.props.accessibilityLabel === 'TheTVDB'));
    all(view, 'LibraryKindTiles')[0].props.onChange('anime'); paint(); await tick(); view = paint();
    all(view, 'Focusable').find((node: any) => node.props.accessibilityLabel === 'AniList').props.onPress();
    all(view, 'FolderSourcePicker')[0].props.onUse({folders: [{path: '/media/anime', name: 'Anime'}]}); view = paint();
    const create = all(view, 'Button').find((node: any) => node.props.children.includes('web.setup.addLibraryScan'));
    assert.equal(create.props.disabled, false); await create.props.onPress();
    assert.equal(f.posts.length, 1); assert.equal(f.posts[0].metadataProvider, 'anilist'); assert.equal(f.posts[0].kind, 'anime');
    assert.equal(f.service.getSnapshot().selectedLibraryId, 'created');
    assert.ok(f.reads.every(path => !path.includes('library-kinds')));
  } finally {f.h.dispose(); f.service.dispose();}
});
