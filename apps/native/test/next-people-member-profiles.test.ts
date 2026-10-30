import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules, Platform} from './react-native-stub.mjs';
const pickerModules = NativeModules as typeof NativeModules & {PorticoImagePicker?: {pick: () => Promise<unknown>}};
import * as profileAvatars from '../src-next/platform/profile-avatars.ts';
import {IdentityClient} from '../../../packages/client-core/src/identity-client.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks, nodes, content, t, theme} from './helpers/owner-server-settings.mjs';

const {MemberProfilesClient} = await screenModule(new URL('../../../packages/client-core/src/member-profiles.ts', import.meta.url), {
  react: {default: {}}, '../../contracts/src/route-paths.ts': await import('../../../packages/contracts/src/route-paths.ts'), '../../contracts/src/routes.ts': await import('../../../packages/contracts/src/routes.ts'),
  './identity-client.ts': await import('../../../packages/client-core/src/identity-client.ts'),
  './profile-management.ts': {PROFILE_ART: ['blue', 'violet', 'mint', 'coral', 'gold', 'slate', 'rose', 'sky']}, './identity-restrictions.ts': await import('../../../packages/client-core/src/identity-restrictions.ts'), './server-messages.ts': await import('../../../packages/client-core/src/server-messages.ts'),
});
const deferred = () => {let resolve!: (value: any) => void, reject!: (value: any) => void; const promise = new Promise<any>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
const profile = (id = 'alex', revision = 4) => ({id, name: 'Alex', art: 'mint', primary: true, position: 0, revision, pinRequired: true, pinRevision: 2, allowedLibraries: [],
  restrictions: {profileId: id, ratingSystem: 'ca', maximumAgeRating: 'PG', maximumAge: 8, allowUnrated: true, blockedLabels: ['Private'], allowDownloads: true, allowLiveTv: false, allowDvr: true, allowWatchTogether: false, revision: 7}});
const systems = [{id: 'ca', name: 'Canada', region: 'CA', screens: [], values: [{code: 'G', label: 'General', minimumAge: 0}, {code: 'PG', label: 'Parental guidance', minimumAge: 8}]}];
const tick = async () => {for (let i = 0; i < 8; i++) await Promise.resolve();};
async function fixture(caller = 'owner', target = 'member', Client = MemberProfilesClient) {
  const requests: any[] = [], reported: unknown[] = [];
  let active = true, personCurrent = true;
  const person = {accountId: 'member', role: target, revision: 5};
  const context = {current: () => active, account: {account: {id: 'caller', role: caller}, capabilities: {managePeople: true}}, session: (): {accessToken: string} | undefined => ({accessToken: 'real-member-bearer'}), api: {mediaUrl: (path: string) => `https://verified.test${path}`, requestRaw(path: string, method: string, options: any) {
    const pending = deferred(); requests.push({path, method, ...options, raw: true, ...pending}); return pending.promise;
  }, request(path: string, method: string, body: unknown, signal: AbortSignal) {
    const pending = deferred(); requests.push({path, method, body, signal, ...pending}); return pending.promise;
  }}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/member-profiles-source.ts', import.meta.url), {
    react: {default: {}}, '../../../../../../packages/client-core/src/member-profiles': {MemberProfilesClient: Client}, '../../../platform/profile-avatars': profileAvatars, '../../../../../../packages/client-core/src/identity-client': {IdentityClient},
    '../lifetime': {screenScopeEnded: () => new Error('ended'), NativeScreenLifetime: class {live = true; current = () => active && this.live; request = (work: any, signal: any) => work(signal); dispose = () => {this.live = false;};}},
  });
  const source = new module.NativeMemberProfilesSource(context, person, () => personCurrent, (error: unknown) => reported.push(error));
  const load = async (items = [profile()]) => {const pending = source.load(); requests.at(-1).resolve({accountId: 'member', items}); await tick(); requests.at(-1).resolve({items: systems}); await pending; return source.getSnapshot().items?.[0];};
  return {source, context, person, requests, reported, load, switchPerson: () => {personCurrent = false;}, retire: () => {active = false;}};
}

test('Profiles reads real rows and server rating scale; Manage requires the server read and exact published row', async () => {
  const f = await fixture();
  assert.equal(f.source.canManage(profile()), false);
  const row = await f.load();
  assert.equal(f.requests[0].path, '/v1/admin/access/members/member/profiles'); assert.equal(f.requests[0].method, 'GET');
  assert.deepEqual(f.source.getSnapshot().items, [profile()]); assert.deepEqual(f.source.getSnapshot().systems, systems);
  assert.equal(f.source.canManage(row), true); assert.equal(f.source.canManage({...row}), false);
  f.source.manage({...row}); assert.equal(f.source.getSnapshot().managing, undefined);
  f.source.manage(row); assert.equal(f.source.getSnapshot().managing, row); f.source.dispose();
});

test('Manage respects owner/admin tiers and never reads owner profiles or another admin as admin', async () => {
  for (const [caller, target, allowed] of [['owner', 'member', true], ['owner', 'admin', true], ['admin', 'member', true], ['admin', 'admin', false], ['admin', 'owner', false], ['owner', 'owner', false], ['member', 'member', false]] as const) {
    const f = await fixture(caller, target);
    if (allowed) {const row = await f.load(); assert.equal(f.source.canManage(row), true);}
    else {await f.source.load(); assert.equal(f.requests.length, 0); assert.equal(f.source.getSnapshot().denied, true);}
    f.source.dispose();
  }
  const f = await fixture(); const read = f.source.load(); f.requests[0].reject({status: 403}); await read;
  assert.equal(f.source.getSnapshot().denied, true); assert.equal(f.source.canManage(profile()), false); f.source.dispose();
});

test('Rename, colour and restrictions save separately with the revision read and preserve all other limits', async () => {
  const f = await fixture(); let row = await f.load(); f.source.manage(row);
  let saving = f.source.rename(row, ' Alex new '); assert.deepEqual(f.requests.at(-1).body, {expectedRevision: 4, name: 'Alex new'});
  f.requests.at(-1).resolve({...profile(), name: 'Alex new', revision: 5}); assert.equal(await saving, true);
  assert.equal(await f.source.colour(row, 'blue'), false, 'the replaced revision cannot save');
  row = f.source.getSnapshot().managing; saving = f.source.colour(row, 'blue'); assert.deepEqual(f.requests.at(-1).body, {expectedRevision: 5, art: 'blue'});
  f.requests.at(-1).resolve({...row, art: 'blue', revision: 6}); await saving;
  row = f.source.getSnapshot().managing; saving = f.source.restrict(row, {allowUnrated: false});
  assert.deepEqual(f.requests.at(-1).body, {expectedRevision: 7, ratingSystem: 'ca', maximumAgeRating: 'PG', allowUnrated: false, blockedLabels: ['Private'], allowDownloads: true, allowLiveTv: false, allowDvr: true, allowWatchTogether: false});
  f.requests.at(-1).resolve({...row, restrictions: {...row.restrictions, allowUnrated: false, revision: 8}}); assert.equal(await saving, true); f.source.dispose();
});

test('a revision conflict re-reads, discards the stale draft, announces changed and never retries the write', async () => {
  const f = await fixture(); const row = await f.load(); f.source.manage(row);
  const saving = f.source.rename(row, 'New'); f.requests.at(-1).reject({status: 409, code: 'profile_changed'}); await tick();
  assert.equal(f.requests.at(-1).method, 'GET'); assert.equal(f.source.getSnapshot().changed, true); assert.equal(f.source.getSnapshot().pinConfirmation, undefined);
  f.requests.at(-1).resolve({accountId: 'member', items: [{...profile(), revision: 9, name: 'Elsewhere'}]}); assert.equal(await saving, false);
  assert.equal(f.source.getSnapshot().managing.name, 'Elsewhere'); assert.equal(f.source.getSnapshot().managing.revision, 9);
  assert.equal(f.requests.filter(request => request.method === 'PATCH').length, 1); f.source.dispose();
});

test('Remove PIN needs the exact confirmation and uses the current read revision', async () => {
  const f = await fixture(); const row = await f.load(); f.source.manage(row);
  assert.equal(await f.source.removePIN(row), false); assert.equal(f.requests.length, 2);
  f.source.askRemovePIN(row); f.source.cancelRemovePIN(); assert.equal(await f.source.removePIN(row), false);
  f.source.askRemovePIN(row); assert.equal(await f.source.removePIN({...row}, f.source.getSnapshot().pinConfirmation), false);
  const confirmation = f.source.getSnapshot().pinConfirmation;
  const saving = f.source.removePIN(row, confirmation); assert.equal(f.requests.at(-1).path, '/v1/admin/access/members/member/profiles/alex/pin-reset'); assert.deepEqual(f.requests.at(-1).body, {expectedRevision: 4});
  assert.equal(await f.source.removePIN(row), false, 'confirmation is consumed before dispatch');
  f.requests.at(-1).resolve({...profile(), revision: 5, pinRequired: false, pinRevision: 3}); assert.equal(await saving, true);
  assert.equal(f.source.getSnapshot().managing.pinRequired, false); f.source.dispose();
});

test('late list, save and conflict re-read cannot publish after switching person or disposing', async () => {
  const first = await fixture(); const reading = first.source.load(); first.switchPerson(); first.requests[0].resolve({accountId: 'member', items: [profile()]}); await reading;
  assert.equal(first.source.getSnapshot().items, undefined); assert.equal(first.requests.length, 1); first.source.dispose();
  for (const conflict of [false, true]) {
    const f = await fixture(); const row = await f.load(); f.source.manage(row); const saving = f.source.rename(row, 'New');
    if (conflict) {f.requests.at(-1).reject({status: 409}); await tick();}
    f.switchPerson(); f.source.dispose(); assert.equal(f.requests.at(-1).signal.aborted, true);
    f.requests.at(-1).resolve(conflict ? {accountId: 'member', items: [profile()]} : {...profile(), name: 'New', revision: 5}); await saving;
    assert.equal(f.source.getSnapshot().managing, undefined); assert.equal(f.reported.length, 0);
  }
});

async function screenFixture(source: any, isTV = false) {
  const host = lifecycleHooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/PeopleMemberProfiles.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../kit/artwork': {ProfileFace: 'ProfileFace'}, '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/theme': {useTheme: () => ({...theme, isTV, type: {body: {}, caption: {}}})},
    '../../../platform/profile-avatars': {...profileAvatars, useAvatarRoute: () => {}}, '../ProfileEditorFields': {ProfileColorPicker: 'ProfileColorPicker', ProfileRestrictionsFields: 'ProfileRestrictionsFields'}, './member-profiles-source': {NativeMemberProfilesSource: class {}},
  });
  const runtime = {text: {t}, error: () => ({title: 'Error'}), reportError: assert.fail};
  return {host, list: () => host.render(() => module.Profiles({source, runtime})), editor: (row: any) => host.render(() => module.MemberProfileEditor({source, runtime, profile: row}))};
}
const byID = (tree: any, id: string) => nodes(tree).find((node: any) => node.props.testID === id)!;

test('phone/tablet rows show real colour faces, chips and Manage; PIN confirmation dispatches only on confirm', async () => {
  const f = await fixture(); const row = await f.load(); const list = await screenFixture(f.source);
  const tree = list.list(); assert.match(content(tree), /Alex/); assert.match(content(tree), /profile.main/); assert.match(content(tree), /profile.protected/);
  const face = nodes(tree).find((node: any) => node.type === 'ProfileFace'); assert.equal(face.props.color, 'mint'); assert.equal(face.props.locked, true);
  byID(tree, 'people-profile-manage-alex').props.onPress();
  const screen = await screenFixture(f.source); byID(screen.editor(row), 'people-profile-remove-pin').props.onPress();
  assert.equal(f.requests.length, 2); assert.equal(byID(screen.editor(row), 'people-profile-pin-confirmation').props.open, true);
  byID(screen.editor(row), 'people-profile-confirm-remove-pin').props.onPress(); assert.equal(f.requests.length, 3);
  f.requests.at(-1).resolve({...profile(), revision: 5, pinRequired: false}); await tick();
  screen.host.dispose(); list.host.dispose(); f.source.dispose();
});

test('a stale PIN confirmation cannot approve a later confirmation for the same read', async () => {
  const f = await fixture(); const row = await f.load(); f.source.manage(row); f.source.askRemovePIN(row);
  const old = f.source.getSnapshot().pinConfirmation; f.source.cancelRemovePIN(); f.source.askRemovePIN(row);
  assert.notEqual(f.source.getSnapshot().pinConfirmation, old); assert.equal(await f.source.removePIN(row, old), false);
  assert.equal(f.requests.length, 2); f.source.dispose();
});

test('retained editor save cannot follow cancellation and reopening for the same person/profile/revision', async () => {
  const f = await fixture(); const row = await f.load(); f.source.manage(row); const screen = await screenFixture(f.source);
  byID(screen.editor(row), 'people-profile-rename').props.onPress();
  byID(screen.editor(row), 'people-profile-name').props.onChangeText('Old draft');
  const oldSave = byID(screen.editor(row), 'people-profile-save').props.onPress;
  const oldSheet = byID(screen.editor(row), 'people-profile-editor'); oldSheet.props.onOpenChange(false);
  byID(screen.editor(row), 'people-profile-rename').props.onPress();
  oldSave(); assert.equal(f.requests.length, 2);
  assert.equal(byID(screen.editor(row), 'people-profile-name').props.value, 'Alex');
  screen.host.dispose(); f.source.dispose();
});

test('Manage disappears if the current caller or target tier no longer allows editing', async () => {
  const f = await fixture(); await f.load(); const screen = await screenFixture(f.source);
  assert.ok(byID(screen.list(), 'people-profile-manage-alex'));
  f.context.account.account.role = 'admin'; f.person.role = 'admin';
  assert.equal(byID(screen.list(), 'people-profile-manage-alex'), undefined);
  f.context.account.account.role = 'owner'; f.person.role = 'owner';
  assert.equal(byID(screen.list(), 'people-profile-manage-alex'), undefined);
  screen.host.dispose(); f.source.dispose();
});


// A command port for the not-yet-mirrored client additions. These tests exercise
// the native scope, picker, real byte-envelope adapter and UI; canonical wire
// parsing/revision validation belongs to the client-core checks run by the lead.
class PictureClient extends MemberProfilesClient {
  constructor(private pictureApi: any, private memberId: string, private pictureUpload: any) {super(pictureApi, memberId);}
  uploadPicture(row: any, body: Uint8Array, contentType: string) {
    return this.pictureUpload(`/v1/admin/access/members/${this.memberId}/profiles/${row.id}/avatar?expectedRevision=${row.revision}`, body, contentType);
  }
  removePicture(row: any, signal: AbortSignal) {
    return this.pictureApi.request(`/v1/admin/access/members/${this.memberId}/profiles/${row.id}/avatar`, 'DELETE', {expectedRevision: row.revision}, signal);
  }
}
const avatar = (version = 2) => ({profileId: 'alex', version, updatedAt: '2026-10-28T12:00:00Z', sourceMime: 'image/jpeg', url: `/v1/admin/access/members/member/profiles/alex/avatar?v=${version}`});
const photo = {base64: '/9j/AA==', bytes: 4, contentType: 'image/jpeg'};
function picker(t: any, pick: () => Promise<unknown>) {
  const old = pickerModules.PorticoImagePicker, oldTV = Platform.isTV;
  pickerModules.PorticoImagePicker = {pick}; Platform.isTV = false;
  t.after(() => {pickerModules.PorticoImagePicker = old; Platform.isTV = oldTV;});
}

test('picture upload and removal publish server read-back and carry the current revision and cancellation signal', async t => {
  let picks = 0; picker(t, async () => {picks++; return photo;});
  const f = await fixture('owner', 'member', PictureClient); let row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); assert.equal(f.source.getSnapshot().busy, true);
  assert.equal(await f.source.choosePicture(row), false); assert.equal(picks, 1); await tick();
  const upload = f.requests.at(-1); assert.equal(upload.path, '/v1/admin/access/members/member/profiles/alex/avatar?expectedRevision=4');
  assert.equal(upload.method, 'POST'); assert.equal(upload.headers['Content-Type'], 'image/jpeg'); assert.equal(upload.signal.aborted, false);
  assert.deepEqual(Buffer.from(upload.body.nativeAvatarBase64, 'base64'), Buffer.from([255, 216, 255, 0]));
  upload.resolve({status: 200, headers: {}, body: {...row, revision: 5, avatar: avatar()}}); assert.equal(await choosing, true);
  row = f.source.getSnapshot().managing; assert.equal(row.avatar.version, 2);
  const removing = f.source.removePicture(row); const remove = f.requests.at(-1);
  assert.equal(remove.path, '/v1/admin/access/members/member/profiles/alex/avatar'); assert.equal(remove.method, 'DELETE'); assert.deepEqual(remove.body, {expectedRevision: 5});
  const {avatar: _avatar, ...without} = row; remove.resolve({...without, revision: 6}); assert.equal(await removing, true);
  assert.equal(f.source.getSnapshot().managing.avatar, undefined); assert.equal(await f.source.removePicture(row), false); f.source.dispose();
});

test('late picker results cannot upload after person, caller authority or screen scope changes', async t => {
  const pick = deferred(); picker(t, () => pick.promise);
  const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); f.switchPerson(); pick.resolve(photo);
  assert.equal(await choosing, false); assert.equal(f.requests.length, 2); f.source.dispose();
  const other = deferred(); pickerModules.PorticoImagePicker = {pick: () => other.promise};
  const g = await fixture('owner', 'member', PictureClient); const managed = await g.load(); g.source.manage(managed);
  const pending = g.source.choosePicture(managed); g.context.account.account.role = 'member'; other.resolve(photo);
  assert.equal(await pending, false); assert.equal(g.requests.length, 2); assert.equal(g.source.getSnapshot().error, undefined); g.source.dispose();
});

test('an in-flight picture upload is aborted on disposal and cannot publish a late result', async t => {
  picker(t, async () => photo);
  const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); await tick(); const upload = f.requests.at(-1);
  f.source.dispose(); assert.equal(upload.signal.aborted, true);
  upload.resolve({status: 200, headers: {}, body: {...row, revision: 5, avatar: avatar()}});
  assert.equal(await choosing, false); assert.equal(f.source.getSnapshot().managing, undefined); assert.equal(f.reported.length, 0);
});

test('cancelled photo choice is quiet; TV, copied rows and ineligible tiers cannot open the picker', async t => {
  let picks = 0; picker(t, async () => {picks++; return null;});
  const f = await fixture('admin', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  assert.equal(await f.source.choosePicture({...row}), false); assert.equal(picks, 0);
  assert.equal(await f.source.choosePicture(row), false); assert.equal(picks, 1); assert.equal(f.requests.length, 2);
  assert.equal(f.source.getSnapshot().busy, false); assert.equal(f.source.getSnapshot().error, undefined);
  Platform.isTV = true; assert.equal(await f.source.choosePicture(row), false); assert.equal(picks, 1); Platform.isTV = false;
  f.person.role = 'admin'; assert.equal(await f.source.choosePicture(row), false); assert.equal(picks, 1); f.source.dispose();
});

test('a picture conflict re-reads the profile, announces changed and never replays the upload', async t => {
  picker(t, async () => photo);
  const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); await tick(); f.requests.at(-1).resolve({status: 409, headers: {}, body: {error: {code: 'profile_changed'}}}); await tick();
  assert.equal(f.requests.at(-1).method, 'GET'); f.requests.at(-1).resolve({accountId: 'member', items: [{...row, revision: 8}]});
  assert.equal(await choosing, false); assert.equal(f.source.getSnapshot().changed, true); assert.equal(f.source.getSnapshot().managing.revision, 8);
  assert.equal(f.requests.filter(request => request.raw).length, 1); f.source.dispose();
});

test('People pictures use the authorized admin URL, bearer and authority cache scope, and both row/editor artwork', async t => {
  picker(t, async () => photo);
  const f = await fixture('owner', 'member', PictureClient); let row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); await tick(); f.requests.at(-1).resolve({status: 200, headers: {}, body: {...row, revision: 5, avatar: avatar()}}); await choosing;
  row = f.source.getSnapshot().managing;
  const picture = f.source.picture(row, 64); assert.equal(picture.uri, 'https://verified.test/v1/admin/access/members/member/profiles/alex/avatar?v=2&size=64');
  assert.deepEqual(picture.headers, {Authorization: 'Bearer real-member-bearer'}); assert.equal(f.source.picture({...row}, 64), undefined);
  const screen = await screenFixture(f.source); assert.equal(byID(screen.editor(row), 'people-profile-picture').props.children[0], 'profile.changePicture');
  assert.ok(byID(screen.editor(row), 'people-profile-remove-picture'));
  assert.equal(nodes(screen.editor(row)).find((node: any) => node.type === 'ProfileFace').props.source.uri, picture.uri);
  const list = await screenFixture(f.source); assert.equal(nodes(list.list()).find((node: any) => node.type === 'ProfileFace').props.source.headers.Authorization, 'Bearer real-member-bearer');
  f.person.role = 'owner'; assert.equal(f.source.picture(row, 44), undefined); f.person.role = 'member';
  f.context.session = () => undefined; assert.equal(f.source.picture(row, 44), undefined);
  screen.host.dispose(); list.host.dispose(); f.source.dispose();
});

test('picture reads refuse public/cross-account URLs, and a denied upload clears the management read', async t => {
  picker(t, async () => photo);
  for (const url of ['/v1/profiles/alex/avatar?v=2', '/v1/admin/access/members/other/profiles/alex/avatar?v=2']) {
    const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
    const choosing = f.source.choosePicture(row); await tick(); f.requests.at(-1).resolve({status: 200, headers: {}, body: {...row, revision: 5, avatar: {...avatar(), url}}}); await choosing;
    assert.equal(f.source.picture(f.source.getSnapshot().managing, 64), undefined); f.source.dispose();
  }
  const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  const choosing = f.source.choosePicture(row); await tick(); f.requests.at(-1).resolve({status: 403, headers: {}, body: {error: {code: 'forbidden'}}});
  assert.equal(await choosing, false); assert.equal(f.source.getSnapshot().denied, true); assert.equal(f.source.getSnapshot().items, undefined);
  assert.equal(f.source.getSnapshot().managing, undefined); assert.equal(f.source.canPickPicture(row), false); f.source.dispose();
});

test('TV editor omits the photo picker, and a retained phone picker action cannot follow a different editor action', async t => {
  let picks = 0; picker(t, async () => {picks++; return photo;});
  const f = await fixture('owner', 'member', PictureClient); const row = await f.load(); f.source.manage(row);
  const tv = await screenFixture(f.source, true); assert.equal(byID(tv.editor(row), 'people-profile-picture'), undefined);
  const phone = await screenFixture(f.source); const choose = byID(phone.editor(row), 'people-profile-picture').props.onPress;
  byID(phone.editor(row), 'people-profile-rename').props.onPress(); choose();
  assert.equal(picks, 0); assert.equal(f.requests.length, 2); tv.host.dispose(); phone.host.dispose(); f.source.dispose();
});
