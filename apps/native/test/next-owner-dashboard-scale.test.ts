import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import type {AdminSession} from '../../../packages/client-core/src/playback-v1/types';
import {kitSelectionFromURL} from '../src-next/dev/gallery-registry.ts';
import {
  appendOwnerSessions, OWNER_COMPACT_ROW_PX, OWNER_STREAM_CARD_LIMIT, ownerCompactWindow, ownerDerivedTotals, ownerStreamNote, ownerStreamTotals, ownerStreamView, ownerUsesCompactRows,
  type OwnerSessionTotals,
} from '../src-next/screens/settings-live/owner-dashboard/model';

const t = defaultI18n.t;
const env = {t, locale: 'en-US'};

function session(overrides: Partial<AdminSession> = {}): AdminSession {
  return Object.freeze({
    id: 's1', user: 'priya.n', profile: 'priya.n', device: 'iPhone', title: 'Spirited Away', kind: 'vod', state: 'playing', positionMs: 3_225_000, durationMs: 7_500_000,
    decision: {video: {action: 'transcode', reasons: []}, audio: {action: 'direct', reasons: []}}, location: 'remote', quality: {mode: 'fixed', maxHeight: 1080}, bandwidthKbps: 8000,
    startedAt: '2026-10-24T15:49:00Z', ...overrides,
  }) as AdminSession;
}

const totals = (value: Partial<OwnerSessionTotals> = {}): OwnerSessionTotals => ({streams: 48, paused: 3, converting: 6, bandwidthKbps: 312_000, people: 37, atHome: 30, away: 7, ...value});

test('the ten-stream cap is the server total, never the ten rows on the page', () => {
  assert.equal(OWNER_STREAM_CARD_LIMIT, 10);
  assert.equal(ownerUsesCompactRows(totals({streams: 10})), false);
  assert.equal(ownerUsesCompactRows(totals({streams: 11})), true);
  assert.equal(ownerUsesCompactRows(totals({streams: 48})), true);
  // No totals means the page cannot prove there are more streams than it holds.
  assert.equal(ownerUsesCompactRows(undefined), false);
  assert.equal(ownerUsesCompactRows(ownerDerivedTotals([session()])), false);
});

test('the three totals count every stream, paused ones included', () => {
  const tiles = ownerStreamTotals(totals(), t);
  assert.deepEqual(tiles.map(tile => [tile.id, tile.value]), [['streams', '48'], ['converting', '6'], ['bandwidth', '312']]);
  assert.equal(tiles[0]!.note, '3 paused');
  assert.equal(tiles[1]!.note, '13% of streams');
  assert.equal(tiles[1]!.tone, 'converting');
  assert.equal(tiles[2]!.unit, 'Mb/s');
  assert.equal(tiles[2]!.note, 'Sent to viewers');
});

test('a quiet server keeps honest zero totals', () => {
  const tiles = ownerStreamTotals(totals({streams: 0, paused: 0, converting: 0, bandwidthKbps: 0}), t);
  assert.equal(tiles[0]!.value, '0');
  assert.equal(tiles[0]!.note, '0 paused');
  assert.equal(tiles[1]!.note, undefined);
  assert.equal(tiles[2]!.value, '0');
  assert.equal(tiles[2]!.unit, 'Mb/s');
});

test('a row note is only what is notable, in the frame order', () => {
  assert.equal(ownerStreamNote(session({state: 'paused', decision: {}}), t)?.id, 'paused');
  // A stream that is both converting and paused says Converted, as the frames order it.
  assert.equal(ownerStreamNote(session({state: 'paused'}), t)?.id, 'converted');
  assert.equal(ownerStreamNote(session({state: 'paused', problem: 'buffering'}), t)?.id, 'buffering');
  assert.equal(ownerStreamNote(session(), t)?.id, 'converted');
  assert.equal(ownerStreamNote(session(), t)?.text, 'Converted');
  assert.equal(ownerStreamNote(session({state: 'buffering', problem: 'behind'}), t)?.text, 'Buffering');
  assert.equal(ownerStreamNote(session({decision: {}}), t), undefined);
});

test('a compact row says the first qualifier the frames draw', () => {
  assert.equal(ownerStreamView(session({item: {id: 'm1', kind: 'movie', title: 'Spirited Away', year: 2001, art: []} as AdminSession['item']}), env).qualifier, '2001');
  assert.equal(ownerStreamView(session({item: {id: 'e1', kind: 'episode', title: 'Long, Long Time', showId: 'show', showTitle: 'The Last of Us', seasonNumber: 1, episodeNumber: 3, art: []} as AdminSession['item']}), env).qualifier, 'S1 E3');
  assert.equal(ownerStreamView(session({kind: 'audio', item: {id: 'song', kind: 'song', title: 'Come Together', artist: 'The Beatles', album: 'Abbey Road', art: []} as AdminSession['item']}), env).qualifier, 'The Beatles');
  assert.equal(ownerStreamView(session({item: undefined}), env).qualifier, undefined);
  assert.equal(ownerStreamView(session({item: undefined}), env).title, 'Spirited Away');
});

test('the windowed list mounts only the rows in view at the fixed pitch', () => {
  const top = ownerCompactWindow(0, 700, 48);
  assert.deepEqual(top, {first: 0, last: 14});
  const middle = ownerCompactWindow(640, 700, 48);
  assert.deepEqual(middle, {first: 7, last: 24});
  const end = ownerCompactWindow(3_072, 700, 48);
  assert.equal(end.last, 47);
  assert.ok((end.last - end.first + 1) < 24);
  assert.equal(OWNER_COMPACT_ROW_PX, 64);
  assert.deepEqual(ownerCompactWindow(0, 700, 0), {first: 0, last: -1});
});

test('Show all appends the server pages in their own order and never repeats a session', () => {
  const first = [session({id: 'a'}), session({id: 'b'})];
  const page = [session({id: 'b'}), session({id: 'c'})];
  const merged = appendOwnerSessions(first, page);
  assert.deepEqual(merged.map(value => value.id), ['a', 'b', 'c']);
  assert.equal(appendOwnerSessions(merged, []), merged);
});

test('the dev gallery can be drawn full-window for a pixel capture', () => {
  assert.deepEqual(kitSelectionFromURL('portico://kit?gallery=shell-owner-dashboard-busy&window=1'), {gallery: 'shell-owner-dashboard-busy', window: true});
  assert.deepEqual(kitSelectionFromURL('portico://kit?gallery=shell-owner-dashboard-busy'), {gallery: 'shell-owner-dashboard-busy'});
  assert.deepEqual(kitSelectionFromURL('portico://kit/'), {});
});


test('the gallery entry refuses malformed URLs before URL normalisation', () => {
  for (const value of [
    'portico://user@kit', 'portico://user:password@kit', 'portico://kit:80',
    'portico://kit#', 'portico://kit#payload', 'portico://kit/path', 'portico://kit?',
    'portico://kit?window=1', 'portico://kit?gallery=', 'portico://kit?gallery=a&window=0',
    'portico://kit?gallery=a&unknown=x', 'portico://kit?gallery=a&gallery=b',
    'portico://kit?gallery=a&window=1&window=1', 'portico://kit?gallery=a&',
    'portico://kit?%67allery=a', 'portico://kit?gallery=%61', 'portico://kit?gallery=a%0A',
    'portico://kit?gallery=' + 'a'.repeat(129), 'portico://KIT', 'PORTICO://kit',
    'portico://%6bit', 'https://kit', 'portico:kit', 'portico:////kit', ' portico://kit',
    'portico://kit\n', 'portico://kit?gallery=a#payload',
  ]) assert.equal(kitSelectionFromURL(value), undefined, value);
  assert.deepEqual(kitSelectionFromURL('portico://kit'), {});
  assert.deepEqual(kitSelectionFromURL('portico://kit/?gallery=shell-owner-dashboard-all&window=1'), {gallery: 'shell-owner-dashboard-all', window: true});
});
