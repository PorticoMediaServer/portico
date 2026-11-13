import assert from 'node:assert/strict';
import test from 'node:test';
import {LibraryManagementService} from '../../packages/client-core/src/library-management.ts';
import {presentError} from '../../packages/client-core/src/presentation/errors.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tick, tree} from './helpers/interaction-harness.mjs';
import * as model from '../src/screens/settings/libraries/model.ts';

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

async function editor(f = fixture()) {
  const onError = (error: unknown) => {throw error;}, onOpenChange = () => {};
  const app = await componentModule(new URL('../src/screens/settings/libraries/AddLibrary.tsx', import.meta.url), {
    react: f.h.react, '@i18n': {defaultI18n: {t: (key: string) => key}}, './model': model,
    '../../../kit/controls': {Button: 'Button', RichSelect: 'RichSelect', TextField: 'TextField'},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'}, './libraries.module.css': {default: {}},
    './FolderPicker': {FolderPicker: 'FolderPicker', ChosenServerFolders: 'ChosenServerFolders'},
  });
  const paint = () => tree(f.h.render(() => app.AddLibrary({open: true, onOpenChange, api: f.api, service: f.service, onError})));
  return {...f, paint};
}

test('web Add library offers server providers and sends the selected TheTVDB before the first scan', async () => {
  const f = await editor(), paint = f.paint;
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


test('web creation resets a forced Local choice, and preserves Local only when the owner selected it', async () => {
  const f = await editor(), paint = f.paint;
  const provider = (view: any[]) => all(view, 'RichSelect').find((node: any) => node.props.options.some((option: any) => option.value === 'local'));
  const kind = async (value: string) => {all(paint(), 'RichSelect')[0].props.onValueChange(value); paint(); await tick(); return paint();};
  try {
    paint(); await tick(); await kind('book'); let view = await kind('tv'); assert.equal(provider(view).props.value, 'tmdb');
    view = await kind('anime'); assert.equal(provider(view).props.value, 'tmdb');
    provider(view).props.onValueChange('local'); await kind('book'); view = await kind('tv'); assert.equal(provider(view).props.value, 'local');
  } finally {f.h.dispose(); f.service.dispose();}
});

test('web creation presents the shared read error and still sends Local only when choices cannot be read', async () => {
  const raw = new Error('Invalid metadata choices. private diagnostic'), base = fixture(); base.fail(raw);
  const f = await editor(base), paint = f.paint;
  try {
    paint(); await tick(); let view = paint();
    assert.ok(JSON.stringify(view).includes(presentError(raw, 'library', {operation: 'load'}).body));
    assert.doesNotMatch(JSON.stringify(view), /private diagnostic|Invalid metadata choices/);
    const provider = all(view, 'RichSelect').find((node: any) => node.props.options.some((option: any) => option.value === 'local'));
    assert.equal(provider.props.value, 'local');
    all(view, 'FolderPicker')[0].props.onChooseMany(['/media/films']); view = paint();
    const create = all(view, 'Dialog')[0].props.footer.props.children[1]; assert.equal(create.props.disabled, false); create.props.onClick(); await tick();
    assert.equal(f.posts[0].metadataProvider, 'local');
  } finally {f.h.dispose(); f.service.dispose();}
});
