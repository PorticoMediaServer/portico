import assert from 'node:assert/strict';
import test from 'node:test';
import {LibraryManagementService} from '../../../packages/client-core/src/library-management.ts';
import {readOwnerKindMetadataAgents} from '../../../packages/client-core/src/owner-library-requests.ts';
import {localMetadataChoices, parseKindMetadataAgents} from '../../../packages/client-core/src/server-admin/metadata-agent.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {all, createRenderer, tick, tree} from '../../../web/test/helpers/interaction-harness.mjs';

function fixture() {
  const h = createRenderer(), posts: any[] = [], reads: string[] = [];
  const local = {id: 'local', available: true};
  const metadataProviders = {
    movies: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, local],
    shows: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, local], books: [local],
    anime: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, {id: 'anilist', available: true}, local],
  };
  let created: any, failure: Error | undefined;
  const api = {request: async <T,>(path: string, method = 'GET', body?: any): Promise<T> => {
    const url = new URL(path, 'https://fixture.invalid');
    if (method === 'POST') {
      posts.push(body); created = {id: 'created', name: body.name, kind: body.kind, revision: '1', scanning: false,
        folders: body.folders.map((path: string) => ({path, available: true})), options: {depth: 'basic'}, metadataProvider: body.metadataProvider};
      return created as T;
    }
    reads.push(url.pathname);
    if (failure && url.pathname === '/v1/admin/libraries') throw failure;
    return (url.pathname === '/v1/admin/libraries' ? {items: created ? [created] : [], metadataProviders}
      : url.pathname === '/v1/admin/jobs' ? {items: []} : created) as T;
  }};
  const service = new LibraryManagementService({api, scope: {serverId: 'fixture', viewerId: 'owner'}, readRetries: 1});
  return {h, posts, reads, api, service, fail: (error: Error) => {failure = error;}};
}

async function editor(f = fixture(), firstRun = true) {
  const core = '../../../../../../packages/client-core/src/';
  const app = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/AddLibraryPage.tsx', import.meta.url), {
    react: f.h.react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    [core + 'owner-library-requests']: {readOwnerKindMetadataAgents}, [core + 'server-admin/metadata-agent']: {parseKindMetadataAgents, localMetadataChoices},
    [core + 'presentation/errors']: {presentError},
    [core + 'presentation/language-list']: {languageName: (code: string) => code},
    '../../../../../../packages/i18n/src/index': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/theme': {useTheme: () => ({colors: {}})}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Focusable': {Focusable: 'Focusable'},
    '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/controls/Select': {Select: 'Select'},
    '../../../kit/controls/FormError': {FormError: 'FormError'}, '../../../platform/keyboard': {FormScrollView: 'FormScrollView'},
    '../../../kit/shell': {SettingsGroup: 'SettingsGroup', SettingsRow: 'SettingsRow'}, './controls': {SectionTitle: 'SectionTitle'},
    './LibraryKindTiles': {LibraryKindTiles: 'LibraryKindTiles'}, './FolderSourcePicker': {FolderSourcePicker: 'FolderSourcePicker'},
    './presentation': {ownerLibraryKind: (kind: string) => ({label: kind})},
  });
  const onCreate = (input: any) => f.service.createLibrary(input), onCancel = () => {};
  const paint = () => tree(f.h.render(() => app.AddLibraryPage({api: f.api, serverName: 'Fixture', onCreate, onCancel, busy: false, firstRun})));
  return {...f, paint};
}

test('phone first-library editor reads choices, changes kind and sends AniList before its first scan', async () => {
  const f = await editor(), paint = f.paint;
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


test('both phone create pages reset forced Local on kind changes but retain an explicit owner choice', async () => {
  for (const firstRun of [false, true]) {
    const f = await editor(fixture(), firstRun), paint = f.paint;
    const checked = (view: any[]) => all(view, 'Focusable').find((node: any) => node.props.accessibilityState?.checked)?.props.accessibilityLabel;
    const kind = async (value: string) => {all(paint(), 'LibraryKindTiles')[0].props.onChange(value); paint(); await tick(); return paint();};
    try {
      paint(); await tick(); let view = paint(); assert.doesNotMatch(JSON.stringify(view), /web.metadataSource.saved/);
      await kind('book'); view = await kind('tv'); assert.equal(checked(view), 'TMDB');
      view = await kind('anime'); assert.equal(checked(view), 'TMDB');
      all(view, 'Focusable').find((node: any) => node.props.accessibilityLabel === 'Local only').props.onPress();
      await kind('book'); view = await kind('tv'); assert.equal(checked(view), 'Local only');
    } finally {f.h.dispose(); f.service.dispose();}
  }
});

test('both phone create pages use shared error words and can create Local only after a provider read fails', async () => {
  for (const firstRun of [false, true]) {
    const raw = new Error('Invalid metadata choices. private diagnostic'), base = fixture(); base.fail(raw);
    const f = await editor(base, firstRun), paint = f.paint;
    try {
      paint(); await tick(); let view = paint();
      assert.deepEqual(all(view, 'FormError')[0].props.children, [presentError(raw, 'library', {operation: 'load'}).body]);
      assert.doesNotMatch(JSON.stringify(view), /private diagnostic|Invalid metadata choices|web.metadataSource.saved/);
      assert.ok(all(view, 'Focusable').some((node: any) => node.props.accessibilityLabel === 'Local only' && node.props.accessibilityState.checked));
      all(view, 'FolderSourcePicker')[0].props.onUse({folders: [{path: '/media/films', name: 'Films'}]}); view = paint();
      const create = all(view, 'Button').find((node: any) => node.props.children.includes(firstRun ? 'web.setup.addLibraryScan' : 'web.empty.addLibrary'));
      assert.equal(create.props.disabled, false); await create.props.onPress(); assert.equal(f.posts[0].metadataProvider, 'local');
    } finally {f.h.dispose(); f.service.dispose();}
  }
});
