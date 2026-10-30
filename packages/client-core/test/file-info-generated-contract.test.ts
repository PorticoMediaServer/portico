import test from 'node:test';
import assert from 'node:assert/strict';
import * as decoders from '../../contracts/src/decoders.ts';
import {parseFileInfoDiscPage, parseFileInfoFilesPage, parseFileInfoGroupsPage, parseFileInfoHeadTracksPage, parseFileInfoMembersPage, parseFileInfoTracksPage, type FileInfoOpening} from '../src/file-info-pages.ts';

// The client's frozen parsers and P2-29's generated contract must agree: every page the client
// accepts is one the generated decoder for that route accepts too.
const opening: FileInfoOpening = {token: 'o1', scope: {serverId: 's1', viewerId: 'v1'}, target: {libraryId: 'l1', itemId: 'm1'}, revision: {catalog: 4, viewer: 2}};
const scope = {serverId: 's1', libraryId: 'l1', itemId: 'm1', viewerFence: 'f1'};
// Real token shapes: a query id is 183.154 characters, a basis 43, a cursor 154.
const queryId = `${'a'.repeat(183)}.${'b'.repeat(154)}`, basis = 'c'.repeat(43), cursor = 'd'.repeat(154);
const envelope = (kind: string, over: Record<string, unknown>) => ({scope, detail: {scope, revision: {catalog: 4, viewer: 2}}, basis, kind, variant: kind.replace(/^file-info-/, ''),
  window: {queryId, limit: 40, returned: 1, nextCursor: cursor}, ...over});
const surface = {fileCount: 2, eligibleFileCount: 2, groupCount: 1, canSplit: true, hasDiscColumns: true, manage: {delete: true, split: true, merge: true}, ownerDiscOccurrence: 'm1.a1'};
const video = {codec: 'hevc', width: 3840, height: 1608, bitDepth: 10, dynamicRange: 'hdr10', frameRate: 23.976};
const file = {id: 'f1', itemId: 'm1', occurrence: 'm1.a1', path: '/m/a.mkv', size: 10, container: 'mkv', duration: 1000, bitRate: 52000000, available: true, partIndex: 0,
  video, disc: {kind: 'bluray', packaging: 'iso', titleNumber: 1, chapterCount: 12}, audioCount: 2, subtitleCount: 1};
const track = {ordinal: 1, codec: 'truehd', language: 'en', channels: 8, channelLayout: '7.1', objectAudio: 'atmos', default: true};

const cases: [string, keyof typeof decoders, Record<string, unknown>, (raw: unknown) => unknown][] = [
  ['files', 'decodeGetItemsIdFileInfoResponse', envelope('file-info-files', {itemKind: 'movie', surface, files: [file]}), raw => parseFileInfoFilesPage(raw, opening)],
  ['groups', 'decodeGetItemFileInfoGroupsResponse', envelope('file-info-groups', {surface, groups: [{id: 'g1', headOccurrence: 'm1.a1', ordinal: 1, memberCount: 1, container: 'mkv',
    sizeBytesExact: '9007199254740993', durationMs: 1000, audioCount: 2, subtitleCount: 1, video}]}), raw => parseFileInfoGroupsPage(raw, opening)],
  ['members', 'decodeGetItemFileInfoMembersResponse', envelope('file-info-members', {groupId: 'g1', total: 1, members: [file]}), raw => parseFileInfoMembersPage(raw, opening, 'g1', true)],
  ['tracks', 'decodeGetItemFileInfoTracksResponse', envelope('file-info-tracks', {lane: 'audio', occurrence: 'm1.a1', total: 1, tracks: [track]}), raw => parseFileInfoTracksPage(raw, opening, 'audio', 'm1.a1')],
  ['head tracks', 'decodeGetItemFileInfoHeadTracksResponse', envelope('file-info-head-tracks', {groupId: 'g1', lane: 'audio', occurrence: 'm1.a1', total: 1, tracks: [{...track, id: 't1'}]}),
    raw => parseFileInfoHeadTracksPage(raw, opening, 'audio', 'g1')],
  ['disc titles', 'decodeGetItemFileInfoDiscTitlesResponse', envelope('file-info-disc-titles', {occurrence: 'm1.a1', discKind: 'bluray', total: 1, titles: [{number: 1, durationMs: 1000, chapters: 12, angles: 1}]}),
    raw => parseFileInfoDiscPage(raw, opening, 'm1.a1')],
];
for (const [name, decoder, raw, parse] of cases) test(`O-074 contract: the ${name} page the client accepts is the generated contract's`, () => {
  assert.doesNotThrow(() => parse(raw), 'the client parser accepts it');
  assert.doesNotThrow(() => (decoders[decoder] as (input: unknown) => unknown)(raw), 'the generated decoder accepts it');
});

// R-09b: every alternative carries a required const `variant`. A variant this client does not know is a newer
// server (never a guess at the shape); a missing one is unreadable.
import {NeedsNewerAppError} from '../src/needs-newer-app.ts';
for (const [name, decoder, raw, parse] of cases) test(`R-09b: the ${name} page reads an unknown variant as a newer server and requires the tag`, () => {
  const unknown = {...(raw as object), variant: 'review'};
  assert.throws(() => parse(unknown), (error: unknown) => error instanceof NeedsNewerAppError && error.tag === 'review');
  // The poll route's union is tagged by variant: its generated decoder reads the unknown tag as an unknown variant.
  assert.deepEqual((decoders.decodeGetFileInfoQueryResponse as (input: unknown) => unknown)(unknown), {variant: 'review', unknownVariant: true});
  assert.doesNotThrow(() => (decoders.decodeGetFileInfoQueryResponse as (input: unknown) => unknown)(raw), 'the tagged page parses through the poll route');
  const {variant: _variant, ...untagged} = raw as Record<string, unknown>;
  assert.throws(() => parse(untagged), (error: unknown) => !(error instanceof NeedsNewerAppError));
});
