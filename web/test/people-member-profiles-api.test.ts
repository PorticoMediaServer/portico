import assert from 'node:assert/strict';
import test from 'node:test';
import {createPeopleMemberProfilesApi} from '../src/screens/settings/people-member-profiles-api.ts';
import type {MemberProfile} from '../../packages/client-core/src/member-profiles.ts';

const profile = {id: 'profile', name: 'Movie night', art: 'blue', primary: false, position: 1, revision: 3, pinRequired: false, pinRevision: 0, allowedLibraries: [],
  restrictions: {profileId: 'profile', ratingSystem: '', maximumAgeRating: '', maximumAge: -1, allowUnrated: true, blockedLabels: [], allowDownloads: true, allowLiveTv: true, allowDvr: true, allowWatchTogether: true, revision: 1}} as const satisfies MemberProfile;
const avatar = {profileId: profile.id, version: 2, updatedAt: '2026-10-28T12:00:00Z', sourceMime: 'image/png', url: '/v1/admin/access/members/person/profiles/profile/avatar?v=2'};

test('People creator sends binary uploads through its own captured transport and removes with the receipt revision', async () => {
  const writes: unknown[] = [], pictures: unknown[] = [];
  const api = createPeopleMemberProfilesApi({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    writes.push({path, method, body, signal}); return {...profile, revision: 5} as T;
  }}, () => true, async (path, body, type) => {writes.push({path, body, type}); return {...profile, revision: 4, avatar};}, {
    memberProfileAvatar: (...args) => {pictures.push(args); return undefined;},
  });
  const bytes = new Blob(['png'], {type: 'image/png'});
  const client = api.forPerson('person');
  const updated = await client.uploadPicture(profile, bytes, bytes.type);
  assert.equal(updated.avatar?.version, 2);
  const signal = new AbortController().signal;
  const removed = await client.removePicture(updated, signal);
  assert.equal(removed.avatar, undefined);
  assert.deepEqual(writes, [
    {path: '/v1/admin/access/members/person/profiles/profile/avatar?expectedRevision=3', body: bytes, type: 'image/png'},
    {path: '/v1/admin/access/members/person/profiles/profile/avatar', method: 'DELETE', body: {expectedRevision: 4}, signal},
  ]);
  api.picture('person', avatar, 160);
  assert.deepEqual(pictures, [['person', avatar, 160]]);
});

test('a retired People workspace sends no request or upload and mints no artwork source', async () => {
  let current = true, writes = 0, pictures = 0;
  const api = createPeopleMemberProfilesApi({request: async <T>() => {writes++; return {} as T;}}, () => current,
    async () => {writes++; return {};}, {memberProfileAvatar: () => {pictures++; return undefined;}});
  const client = api.forPerson('person'); current = false;
  await assert.rejects(client.uploadPicture(profile, new Uint8Array([1]), 'image/png'), {name: 'AbortError'});
  await assert.rejects(client.removePicture(profile), {name: 'AbortError'});
  await assert.rejects(client.list(), {name: 'AbortError'});
  await assert.rejects(api.ratingSystems(), {name: 'AbortError'});
  assert.equal(api.picture('person', avatar, 64), undefined);
  assert.equal(writes, 0); assert.equal(pictures, 0);
});

test('an upload completing after People retires cannot publish its receipt', async () => {
  let current = true, finish!: (value: unknown) => void;
  const delivery = new Promise(resolve => {finish = resolve;});
  const api = createPeopleMemberProfilesApi({request: async <T>() => ({} as T)}, () => current, () => delivery, {memberProfileAvatar: () => undefined});
  const uploading = api.forPerson('person').uploadPicture(profile, new Uint8Array([1]), 'image/png');
  current = false; finish({...profile, avatar});
  await assert.rejects(uploading, {name: 'AbortError'});
});

test('request delivery is fenced after retirement and aborted removals never reach the transport', async () => {
  let current = true, writes = 0, finish!: (value: unknown) => void;
  const delivery = new Promise(resolve => {finish = resolve;});
  const api = createPeopleMemberProfilesApi({request: async <T>() => {writes++; return await delivery as T;}}, () => current,
    async () => ({}), {memberProfileAvatar: () => undefined});
  const client = api.forPerson('person'), aborted = new AbortController(); aborted.abort();
  await assert.rejects(client.removePicture(profile, aborted.signal), {name: 'AbortError'});
  assert.equal(writes, 0);
  const removing = client.removePicture(profile); current = false; finish(profile);
  await assert.rejects(removing, {name: 'AbortError'});
  assert.equal(writes, 1);
});
