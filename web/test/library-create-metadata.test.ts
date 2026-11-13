import assert from 'node:assert/strict';
import test from 'node:test';
import {LibraryManagementService} from '../../packages/client-core/src/library-management.ts';
import {readOwnerKindMetadataAgents} from '../../packages/client-core/src/owner-library-requests.ts';
import {parseKindMetadataAgents} from '../../packages/client-core/src/server-admin/metadata-agent.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tick, tree} from './helpers/interaction-harness.mjs';
import * as model from '../src/screens/settings/libraries/model.ts';

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

test('web Add library offers server providers and sends the selected TheTVDB before the first scan', async () => {
  const f = fixture(), onError = (error: unknown) => {throw error;}, onOpenChange = () => {};
  const app = await componentModule(new URL('../src/screens/settings/libraries/AddLibrary.tsx', import.meta.url), {
    react: f.h.react, '@i18n': {defaultI18n: {t: (key: string) => key}}, './model': model,
    '../../../kit/controls': {Button: 'Button', RichSelect: 'RichSelect', TextField: 'TextField'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'}, './libraries.module.css': {default: {}},
    './FolderPicker': {FolderPicker: 'FolderPicker', ChosenServerFolders: 'ChosenServerFolders'},
  });
  const paint = () => tree(f.h.render(() => app.AddLibrary({open: true, onOpenChange, api: f.api, service: f.service, onError})));
  try {
    paint(); await tick(); let view = paint();
    const picker = all(view, 'RichSelect').find(node => node.props.options.some((option: any) => option.value === 'tvdb'));
    assert.deepEqual(picker.props.options.map((option: any) => option.value), ['tmdb', 'tvdb', 'local']);
    picker.props.onValueChange('tvdb');
    all(view, 'FolderPicker')[0].props.onChooseMany(['/media/films']); view = paint();
    const create = all(view, 'Dialog')[0].props.footer.props.children[1];
    assert.equal(create.props.disabled, false); create.props.onClick(); await tick();
    assert.equal(f.posts.length, 1); assert.equal(f.posts[0].metadataProvider, 'tvdb');
    assert.equal(f.service.getSnapshot().selectedLibraryId, 'created');
    assert.equal(f.reads[0], '/v1/admin/libraries');
  } finally {f.h.dispose(); f.service.dispose();}
});

test('phone first-library editor reads choices, changes kind and sends AniList before its first scan', async () => {
  const f = fixture(), core = '../../../../../../packages/client-core/src/';
  const app = await componentModule(new URL('../../apps/native/src-next/screens/settings-live/owner-libraries/AddLibraryPage.tsx', import.meta.url), {
    react: f.h.react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    [core + 'owner-library-requests']: {readOwnerKindMetadataAgents}, [core + 'server-admin/metadata-agent']: {parseKindMetadataAgents},
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
    assert.ok(all(view, 'Focusable').some(node => node.props.accessibilityLabel === 'TheTVDB'));
    all(view, 'LibraryKindTiles')[0].props.onChange('anime'); paint(); await tick(); view = paint();
    all(view, 'Focusable').find(node => node.props.accessibilityLabel === 'AniList').props.onPress();
    all(view, 'FolderSourcePicker')[0].props.onUse({folders: [{path: '/media/anime', name: 'Anime'}]}); view = paint();
    const create = all(view, 'Button').find(node => node.props.children.includes('web.setup.addLibraryScan'));
    assert.equal(create.props.disabled, false); await create.props.onPress();
    assert.equal(f.posts.length, 1); assert.equal(f.posts[0].metadataProvider, 'anilist'); assert.equal(f.posts[0].kind, 'anime');
    assert.equal(f.service.getSnapshot().selectedLibraryId, 'created');
    assert.ok(f.reads.every(path => !path.includes('library-kinds')));
  } finally {f.h.dispose(); f.service.dispose();}
});
