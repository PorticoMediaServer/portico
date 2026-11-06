import assert from 'node:assert/strict';
import test from 'node:test';
import {HttpLocalApi} from '../src/index.ts';
import {profilePictureSource, resolvePublishedDownloadUrl} from '../src/media-sources.ts';
import {resolveShellMediaUrl} from '../src/shell-media.ts';

const avatar = {profileId: 'profile', version: 2, updatedAt: '2026-11-05T00:00:00Z', sourceMime: 'image/png', url: '/v1/direct/profiles/profile/avatar?v=2'};

test('published download and shell media resolution retain the core origin fence and exact grant query', () => {
  const api = new HttpLocalApi('https://original.test', 'token');
  const path = '/v1/media/content/grant/media?capability=original%2Fgrant&part=2';
  for (const resolve of [resolvePublishedDownloadUrl, resolveShellMediaUrl]) {
    assert.equal(resolve(api, path), 'https://original.test' + path);
    assert.throws(() => resolve(api, 'https://other.test' + path), /Cross-origin/);
    assert.throws(() => resolve(api, '//other.test' + path), /Cross-origin/);
  }
});

test('profile pictures preserve rounded size, bearer header and refusal of a foreign capability', () => {
  const api = new HttpLocalApi('https://original.test', 'token');
  assert.deepEqual(profilePictureSource(api, 'current-token', avatar, 159.6), {
    uri: 'https://original.test' + avatar.url + '&size=160', headers: {Authorization: 'Bearer current-token'},
  });
  assert.equal(profilePictureSource(api, undefined, avatar, 160), undefined);
  assert.equal(profilePictureSource(api, 'current-token', {...avatar, url: 'https://other.test/picture'}, 160), undefined);
  assert.equal(profilePictureSource(undefined, 'current-token', avatar, 160), undefined);
});
