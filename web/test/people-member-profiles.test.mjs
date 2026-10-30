import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, all, textOf} from './helpers/interaction-harness.mjs';
import {MemberProfilesClient} from '../../packages/client-core/src/member-profiles.ts';
import {defaultI18n} from '../../packages/i18n/src/index.ts';

// OWN-001: a person's Profiles tab lists their real profiles, and Manage edits
// one through the owner-scoped routes, revision-fenced, never the viewer's own.

const restrictions = (profileId, patch = {}) => ({profileId, ratingSystem: '', maximumAgeRating: '', maximumAge: -1, allowUnrated: true, blockedLabels: [],
  allowDownloads: true, allowLiveTv: true, allowDvr: true, allowWatchTogether: true, revision: 1, ...patch});
const profile = (id, patch = {}) => ({id, name: id === 'main' ? 'Nana' : 'Movie night', art: 'coral', primary: id === 'main', position: id === 'main' ? 0 : 1, revision: 3,
  pinRequired: id !== 'main', pinRevision: 1, allowedLibraries: [], restrictions: restrictions(id), ...patch});
const systems = [{id: 'mpaa', name: 'MPA', values: [{code: 'G', label: 'G', minimumAge: 0}, {code: 'PG', label: 'PG', minimumAge: 8}]}];

const avatar = (version = 1) => ({profileId: 'movies', version, updatedAt: '2026-10-28T12:00:00Z', sourceMime: 'image/png', url: `/v1/admin/access/members/nana/profiles/movies/avatar?v=${version}`});

async function mount({withPicture = false, canEdit = true, uploadFailure, uploaded} = {}) {
  const calls = [];
  const request = async (path, method = 'GET', body) => {
    calls.push({path, method, body});
    if (method === 'GET') return {accountId: 'nana', items: [profile('main'), profile('movies', withPicture ? {avatar: avatar()} : {})]};
    if (method === 'DELETE') return profile('movies', {revision: 5});
    if (method === 'PATCH') return profile('movies', {name: body.name ?? 'Movie night', revision: 4});
    if (method === 'PUT') return profile('movies', {name: 'Film club', revision: 4, restrictions: restrictions('movies', {ratingSystem: body.ratingSystem, maximumAgeRating: body.maximumAgeRating, maximumAge: 8, revision: 2})});
    return profile('movies', {pinRequired: false, revision: 4});
  };
  const pictureCalls = [];
  const upload = async (path, body, contentType) => {
    calls.push({path, method: 'UPLOAD', body, contentType});
    if (uploadFailure) throw uploadFailure;
    return uploaded ? uploaded : profile('movies', {revision: 4, avatar: avatar(2)});
  };
  const api = {forPerson: accountId => new MemberProfilesClient({request}, accountId, upload), ratingSystems: async () => systems,
    picture: (accountId, avatar, pixels) => {pictureCalls.push({accountId, avatar, pixels}); return avatar ? {key: avatar.url} : undefined;}};
  const h = createRenderer();
  const passthrough = name => props => ({type: name, props});
  const m = await componentModule(new URL('../src/screens/settings/PeopleMemberProfiles.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '@design': {profileArtColors: new Proxy({}, {get: () => '#000'})},
    '@core/presentation/errors.ts': {presentError: error => ({body: String(error?.message ?? error)})},
    '../../kit/controls': {Button: 'Button', RichSelect: 'RichSelect', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/artwork': {Artwork: 'Artwork'},
    '../../kit/overlays/Dialog': {Dialog: props => ({type: 'Dialog', props: {title: props.title, open: props.open, children: props.open ? [props.children, props.footer] : null}})},
    '../../kit/settings': {SettingsAction: 'SettingsAction', SettingsStringList: passthrough('StringList')},
    './account-profile/profile.module.css': {default: new Proxy({}, {get: (_, key) => String(key)})},
    './people-member-profiles.module.css': {default: new Proxy({}, {get: (_, key) => String(key)})},
  });
  const render = () => tree(h.render(() => m.PeopleMemberProfiles({api, accountId: 'nana', canEdit})));
  const settle = async (turns = 8) => {let value; for (let i = 0; i < turns; i++) {value = render(); await new Promise(resolve => setTimeout(resolve, 0));} return value;};
  return {calls, pictureCalls, settle, dispose: h.dispose};
}
const button = (rendered, label) => all(rendered, 'Button').filter(node => textOf(node) === label);

test('the Profiles tab lists the person\'s real profiles with what sets each apart, and Manage edits through the owner routes', async () => {
  const app = await mount();
  let value = await app.settle();
  assert.equal(app.calls[0].path, '/v1/admin/access/members/nana/profiles');
  const text = textOf(value);
  for (const expected of ['Nana', 'Movie night', 'Main profile', 'Protected by a PIN']) assert.ok(text.includes(expected), expected);
  const manage = button(value, 'Manage');
  assert.equal(manage.length, 2);
  manage[1].props.onClick();
  value = await app.settle();
  const dialog = all(value, 'Dialog').find(node => node.props.open && node.props.title === 'Movie night');
  assert.ok(dialog, 'Manage opens the editor for that profile');
  all(tree(dialog), 'TextField')[0].props.onChange({currentTarget: {value: 'Film club'}});
  value = await app.settle(2);
  all(value, 'RichSelect')[0].props.onValueChange('PG');
  value = await app.settle(2);
  button(value, 'Save')[0].props.onClick();
  await app.settle();
  const writes = app.calls.filter(call => call.method !== 'GET');
  assert.deepEqual(writes.map(call => [call.method, call.path]), [
    ['PATCH', '/v1/admin/access/members/nana/profiles/movies'],
    ['PUT', '/v1/admin/access/members/nana/profiles/movies/restrictions'],
  ]);
  assert.deepEqual(writes[0].body, {expectedRevision: 3, name: 'Film club'});
  assert.equal(writes[1].body.expectedRevision, 1);
  assert.equal(writes[1].body.ratingSystem, 'mpaa');
  assert.equal(writes[1].body.maximumAgeRating, 'PG');
});

test('removing a PIN asks first and then clears it through the owner route', async () => {
  const app = await mount();
  let value = await app.settle();
  button(value, 'Manage')[1].props.onClick();
  value = await app.settle();
  button(value, 'Remove PIN')[0].props.onClick();
  value = await app.settle(2);
  assert.equal(app.calls.some(call => call.method === 'POST'), false, 'nothing is cleared before the confirmation');
  const confirm = all(value, 'SettingsAction').find(node => textOf(node) === 'Remove PIN');
  confirm.props.onClick();
  await app.settle();
  const reset = app.calls.find(call => call.method === 'POST');
  assert.deepEqual(reset, {path: '/v1/admin/access/members/nana/profiles/movies/pin-reset', method: 'POST', body: {expectedRevision: 3}});
});

const pick = (value, file, expectedOpened = true) => {
  const input = all(value, 'input').find(node => node.props.type === 'file');
  let opened = false;
  input.props.ref.current = {click: () => {opened = true;}};
  button(value, 'Choose a photo…')[0].props.onClick();
  // A pending write keeps the browser chooser shut as well as refusing bytes.
  assert.equal(opened, expectedOpened);
  const target = {files: [file], value: 'selected'};
  input.props.onChange({currentTarget: target});
  assert.equal(target.value, '', 'the same file can be selected again');
};

test('a picture upload updates list and editor immediately, preserves draft edits and uses the new revision for removal', async () => {
  const app = await mount();
  let value = await app.settle();
  button(value, 'Manage')[1].props.onClick();
  value = await app.settle();
  all(value, 'TextField')[0].props.onChange({currentTarget: {value: 'Film club'}});
  value = await app.settle(2);
  const file = new File([new Uint8Array([1, 2])], 'photo.png', {type: 'image/png'});
  pick(value, file);
  value = await app.settle();
  const upload = app.calls.find(call => call.method === 'UPLOAD');
  assert.equal(upload.path, '/v1/admin/access/members/nana/profiles/movies/avatar?expectedRevision=3');
  assert.equal(upload.body, file);
  assert.equal(upload.contentType, 'image/png');
  assert.equal(all(value, 'TextField')[0].props.value, 'Film club');
  assert.equal(all(value, 'Dialog').filter(node => node.props.open).length, 1);
  const pictures = all(value, 'Artwork').filter(node => node.props.source);
  assert.equal(pictures.length, 2, 'the person list and open editor both show the receipt');
  assert.ok(pictures.every(node => node.props.source.key === avatar(2).url));
  assert.ok(app.pictureCalls.some(call => call.accountId === 'nana' && call.avatar?.version === 2 && call.pixels === 160));
  button(value, 'Remove')[0].props.onClick();
  value = await app.settle();
  assert.deepEqual(app.calls.find(call => call.method === 'DELETE'), {path: '/v1/admin/access/members/nana/profiles/movies/avatar', method: 'DELETE', body: {expectedRevision: 4}});
  assert.equal(all(value, 'Artwork').some(node => node.props.source), false);
  assert.equal(button(value, 'Remove').length, 0);
  assert.equal(all(value, 'TextField')[0].props.value, 'Film club');
  button(value, 'Save')[0].props.onClick();
  await app.settle();
  assert.deepEqual(app.calls.find(call => call.method === 'PATCH').body, {expectedRevision: 5, name: 'Film club'});
  app.dispose();
});

test('the picker refuses WebP, empty and oversized files before sending bytes; accepts JPEG at the limit', async () => {
  const app = await mount();
  let value = await app.settle();
  button(value, 'Manage')[1].props.onClick();
  value = await app.settle();
  assert.equal(all(value, 'input').find(node => node.props.type === 'file').props.accept, 'image/jpeg,image/png');
  for (const file of [new File(['x'], 'image.webp', {type: 'image/webp'}), new File([], 'empty.png', {type: 'image/png'}),
    new File([new Uint8Array(4 * 1024 * 1024 + 1)], 'large.png', {type: 'image/png'})]) {
    pick(value, file);
    value = await app.settle(2);
    assert.equal(app.calls.some(call => call.method === 'UPLOAD'), false);
    assert.ok(value.some(node => node.props.role === 'alert'));
  }
  pick(value, new File([new Uint8Array(4 * 1024 * 1024)], 'limit.jpg', {type: 'image/jpeg'}));
  await app.settle();
  assert.equal(app.calls.filter(call => call.method === 'UPLOAD').length, 1);
  app.dispose();
});

test('failed picture uploads keep the last confirmed picture and editor available', async () => {
  const app = await mount({withPicture: true, uploadFailure: new Error('Picture unavailable')});
  let value = await app.settle();
  button(value, 'Manage')[1].props.onClick();
  value = await app.settle();
  pick(value, new File(['x'], 'photo.png', {type: 'image/png'}));
  value = await app.settle();
  assert.ok(textOf(value).includes('Picture unavailable'));
  assert.equal(all(value, 'Artwork').filter(node => node.props.source?.key === avatar().url).length, 2);
  assert.equal(button(value, 'Choose a photo…')[0].props.disabled, false);
  app.dispose();
});

test('read-only profiles still display authenticated pictures and offer no editor', async () => {
  const app = await mount({withPicture: true, canEdit: false});
  const value = await app.settle();
  assert.equal(all(value, 'Artwork').filter(node => node.props.source?.key === avatar().url).length, 1);
  assert.equal(button(value, 'Manage').length, 0);
  assert.equal(all(value, 'input').length, 0);
  app.dispose();
});

test('one pending upload excludes duplicate picture writes and late completion after unmount', async () => {
  let finish;
  const uploaded = new Promise(resolve => {finish = resolve;});
  const app = await mount({uploaded});
  let value = await app.settle();
  button(value, 'Manage')[1].props.onClick();
  value = await app.settle();
  const file = new File(['x'], 'photo.png', {type: 'image/png'});
  pick(value, file);
  pick(value, file, false);
  assert.equal(app.calls.filter(call => call.method === 'UPLOAD').length, 1);
  value = await app.settle(2);
  assert.equal(button(value, 'Choose a photo…')[0].props.disabled, true);
  app.dispose();
  finish(profile('movies', {revision: 4, avatar: avatar(2)}));
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(app.calls.filter(call => call.method === 'UPLOAD').length, 1);
});
