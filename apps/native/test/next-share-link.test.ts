import assert from 'node:assert/strict';
import test from 'node:test';
import {decideShareLink, sharedRouteToWebUrl, type ShareParser} from '../src-next/screens/share/share-link.ts';

// A stand-in for client-core's parser (P2-61 owns the real one): it only proves the decision
// never reads the destination itself and passes it through untouched.
const parse: ShareParser<{kind: string; id: string}> = url => {
  const match = /^https:\/\/web\.getportico\.tv\/s\/([^/]+)\/(item|person|library)\/([^/]+)$/.exec(url);
  return match ? {serverId: match[1]!, destination: {kind: match[2]!, id: match[3]!}} : undefined;
};

test('the forwarded app route maps back to exactly one web URL, and nothing malformed passes', () => {
  assert.equal(sharedRouteToWebUrl('portico://shared/srv_1/item/abc-1'), 'https://web.getportico.tv/s/srv_1/item/abc-1');
  assert.equal(sharedRouteToWebUrl('portico://shared/srv_1'), 'https://web.getportico.tv/s/srv_1', 'the server alone is its Home');
  assert.equal(sharedRouteToWebUrl('portico://shared/srv_1/search?q=slow%20horses'), 'https://web.getportico.tv/s/srv_1/search?q=slow%20horses');
  for (const raw of ['portico://shared/', 'portico://shared/srv_1/item/abc#t=1', 'portico://shared/srv_1//abc', 'portico://shared/../item/abc',
    'portico://shared/srv_1/item/a%2Fb', 'portico://shared/srv_1/item/a%0Ab', 'portico://item/abc', 'portico://shared/a/b/c/d/e/f/g',
    `portico://shared/srv_1/item/${'a'.repeat(513)}`, 'portico://shared/srv_1/x?q=%0A', 'https://web.getportico.tv/s/srv_1/item/abc', 42, undefined]) {
    assert.equal(sharedRouteToWebUrl(raw), undefined, String(raw));
  }
});

test('the real client-core parser reads what native forwards, and never a timestamp', async () => {
  const {parseServerLinkTarget, buildServerLink} = await import('../../../packages/client-core/src/server-links.ts');
  const target = parseServerLinkTarget(sharedRouteToWebUrl('portico://shared/srv_1/item/abc?t=120')!);
  assert.deepEqual(target, {serverId: 'srv_1', destination: 'item', params: {itemId: 'abc'}});
  assert.equal(buildServerLink(target!), 'https://web.getportico.tv/s/srv_1/item/abc', 'Share and Copy link use the same builder');
  assert.equal(parseServerLinkTarget(sharedRouteToWebUrl('portico://shared/srv_1')!)?.destination, 'home');
});

test('a link for the open server pushes its destination; another signed-in server switches first', () => {
  const url = 'https://web.getportico.tv/s/srv_1/item/abc';
  assert.deepEqual(decideShareLink(url, parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1'], online: true}),
    {kind: 'open', serverId: 'srv_1', destination: {kind: 'item', id: 'abc'}});
  assert.deepEqual(decideShareLink(url, parse, {currentServerId: 'srv_2', savedServerIds: ['srv_2', 'srv_1'], online: true}),
    {kind: 'switch', serverId: 'srv_1', destination: {kind: 'item', id: 'abc'}});
});

test('signed out, the link waits for sign-in and a profile, then decides again', () => {
  const url = 'https://web.getportico.tv/s/srv_1/person/p9';
  const first = decideShareLink(url, parse, {savedServerIds: [], online: true});
  assert.deepEqual(first, {kind: 'sign-in', serverId: 'srv_1', destination: {kind: 'person', id: 'p9'}});
  // After signing in to that server and choosing a profile, the same link opens.
  assert.equal(decideShareLink(url, parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1'], online: true}).kind, 'open');
});

test('no access, offline and unknown links get the plain message, never a blank screen', () => {
  const url = 'https://web.getportico.tv/s/srv_9/item/abc';
  assert.deepEqual(decideShareLink(url, parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1'], online: true}), {kind: 'unavailable', reason: 'no-access'});
  assert.deepEqual(decideShareLink(url, parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1', 'srv_9'], online: false}), {kind: 'unavailable', reason: 'offline'});
  assert.deepEqual(decideShareLink('https://web.getportico.tv/s/srv_1/unknown-kind/x', parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1'], online: true}), {kind: 'unavailable', reason: 'not-a-share'});
  assert.deepEqual(decideShareLink(undefined, parse, {currentServerId: 'srv_1', savedServerIds: ['srv_1'], online: true}), {kind: 'unavailable', reason: 'not-a-share'});
});
