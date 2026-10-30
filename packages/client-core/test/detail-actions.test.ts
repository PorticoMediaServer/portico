import test from 'node:test';
import assert from 'node:assert/strict';
import {detailActions} from '../src/presentation/index.ts';
import {enUS} from '../../i18n/src/index.ts';
import {isIconId} from '../../design/src/index.ts';

const personal = (over: Partial<Record<string, unknown>> = {}) => ({
  watchlisted: false,
  favorite: false,
  rating: null,
  revision: 0,
  watched: false,
  reaction: 'none' as const,
  progressSeconds: 0,
  lastPlayedAt: '',
  status: 'current' as const,
  conflicts: [],
  ...over,
});

const item = (over: Partial<Record<string, unknown>> = {}) => ({
  id: 'item',
  libraryId: 'lib',
  title: 'Test Movie',
  kind: 'movie',
  duration: 7200,
  progressSeconds: 0,
  available: true,
  ...over,
});

const action = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  labelKey: `action.${id}`,
  enabled: true,
  ...extra,
});

test('CON-25: table test of detailActions', () => {
  // Movie with progress: Resume with remaining time; the row is My List · Favorite · Watched.
  const withProgress = detailActions({
    item: item({progressSeconds: 2520, duration: 7200}),
    personal: personal({watchlisted: true, favorite: false, progressSeconds: 2520}),
    actions: [
      action('watchlist'),
      action('favorite'),
      action('watched'),
      action('play', {playback: {itemId: 'item', startSeconds: 2520}}),
      action('start_over', {playback: {itemId: 'item', startSeconds: 0}}),
      action('add_to_playlist'),
    ],
  }, {owner: true, local: true, platform: 'web'});
  assert.equal(withProgress.primary?.id, 'resume');
  assert.equal(withProgress.primary?.label, 'entry.resume');
  assert.ok(withProgress.primary?.remaining, 'remaining time present');
  assert.equal(withProgress.primary?.caption, `${withProgress.primary?.remaining} left`);
  assert.deepEqual(withProgress.quick.map(q => q.id), ['watchlist', 'favorite', 'watched']);
  assert.equal(withProgress.quick[0]!.selected, true);
  const moreIds = withProgress.more.flatMap(g => g.actions.map(a => a.id));
  assert.ok(moreIds.includes('playFromStart'), 'Play from beginning in More');
  assert.ok(!moreIds.includes('watched'), 'Watched is on the row, not repeated in More');
  assert.ok(!moreIds.includes('play') && !moreIds.includes('resume'), 'primary not duplicated in More');
  assert.ok(!moreIds.includes('watchlist') && !moreIds.includes('favorite'), 'quick not duplicated in More');

  // Movie without progress: Play, no caption.
  const without = detailActions({
    item: item({progressSeconds: 0}),
    personal: personal(),
    actions: [
      action('watchlist'),
      action('favorite'),
      action('play', {playback: {itemId: 'item', startSeconds: 0}}),
    ],
  }, {platform: 'web'});
  assert.equal(without.primary?.id, 'play');
  assert.equal(without.primary?.label, 'entry.play');
  assert.equal(without.primary?.caption, undefined);
  assert.deepEqual(without.quick.map(q => q.id), ['watchlist', 'favorite']);
  assert.deepEqual(without.more.flatMap(g => g.actions.map(a => a.id)).includes('playFromStart'), false);

  // Episode: same shape, Resume when there is progress.
  const episode = detailActions({
    item: item({kind: 'episode', progressSeconds: 600, duration: 2400}),
    personal: personal({progressSeconds: 600}),
    actions: [
      action('watchlist'),
      action('favorite'),
      action('play', {playback: {itemId: 'item', startSeconds: 600}}),
      action('start_over', {playback: {itemId: 'item', startSeconds: 0}}),
    ],
  }, {platform: 'phone'});
  assert.equal(episode.primary?.id, 'resume');
  assert.deepEqual(episode.quick.map(q => q.id), ['watchlist', 'favorite']);

  // Server withholds Favorite: quick has only My List.
  const noFavorite = detailActions({
    item: item(),
    personal: personal(),
    actions: [action('watchlist'), action('play', {playback: {itemId: 'item', startSeconds: 0}})],
  }, {platform: 'web'});
  assert.deepEqual(noFavorite.quick.map(q => q.id), ['watchlist']);
});

test('CON-25 follow-up: rate in More when the server offers rating', () => {
  const base = (rating: number | null, extra: ReturnType<typeof action>[] = []) => detailActions({
    item: item(),
    personal: personal({rating}),
    actions: [
      action('watchlist'),
      action('favorite'),
      action('watched'),
      action('rating'),
      action('play', {playback: {itemId: 'item', startSeconds: 0}}),
      ...extra,
    ],
  }, {platform: 'web'});
  // Rated: personal group ends with rate, label title.rated, filled star.
  const rated = base(4);
  const ratedPersonal = rated.more.find(g => g.id === 'personal')!;
  assert.ok(ratedPersonal, 'personal group present');
  assert.equal(ratedPersonal.actions.at(-1)!.id, 'rate');
  assert.equal(ratedPersonal.actions.at(-1)!.label, 'title.rated');
  assert.equal(ratedPersonal.actions.at(-1)!.icon, 'starFilled');
  // Unrated: same slot, entry.rate label, outline star.
  const unrated = base(null);
  const unratedPersonal = unrated.more.find(g => g.id === 'personal')!;
  assert.equal(unratedPersonal.actions.at(-1)!.id, 'rate');
  assert.equal(unratedPersonal.actions.at(-1)!.label, 'entry.rate');
  assert.equal(unratedPersonal.actions.at(-1)!.icon, 'star');
  // Server withholds rating: no rate anywhere.
  const withheld = detailActions({
    item: item(),
    personal: personal({rating: 4}),
    actions: [action('watchlist'), action('favorite'), action('play', {playback: {itemId: 'item', startSeconds: 0}})],
  }, {platform: 'web'});
  assert.ok(!withheld.more.flatMap(g => g.actions.map(a => a.id)).includes('rate' as never), 'no rate when withheld');
  // Rating alone still yields a personal group (no watched/reaction offered).
  const lone = detailActions({
    item: item(),
    personal: personal({rating: null}),
    actions: [action('watchlist'), action('rating'), action('play', {playback: {itemId: 'item', startSeconds: 0}})],
  }, {platform: 'web'});
  assert.deepEqual(lone.more.find(g => g.id === 'personal')?.actions.map(a => a.id), ['rate']);
});

test('CON-25: labels are catalogue IDs and glyphs are registry IDs', () => {
  const result = detailActions({
    item: item({progressSeconds: 100, duration: 1000}),
    personal: personal(),
    actions: [
      action('watchlist'),
      action('favorite'),
      action('watched'),
      action('reaction'),
      action('play', {playback: {itemId: 'item', startSeconds: 100}}),
      action('start_over', {playback: {itemId: 'item', startSeconds: 0}}),
      action('add_to_playlist'),
      action('add_to_collection'),
    ],
  }, {owner: true, local: true});
  assert.ok(result.primary!.label in enUS);
  assert.ok(isIconId(result.primary!.icon));
  for (const q of result.quick) {
    assert.ok(q.label in enUS, q.label);
    assert.ok(isIconId(q.icon), q.icon);
  }
  for (const g of result.more) {
    for (const a of g.actions) {
      assert.ok(a.label in enUS, a.label);
      assert.ok(isIconId(a.icon), a.icon);
    }
  }
  // US spelling: Favorite, never Favourite in labels.
  for (const q of result.quick) {
    assert.ok(!enUS[q.label].includes('Favourite'), enUS[q.label]);
  }
});

test('the title page offers what its card offers: queue, group, download and delete follow the same answers', () => {
  const page = (viewer: Parameters<typeof detailActions>[1]) => detailActions({
    item: {id: 'm1', kind: 'movie', libraryId: 'lib', duration: 6000, progressSeconds: 0},
    personal: personal(),
    actions: [action('play', {playback: {itemId: 'm1'}})],
  } as Parameters<typeof detailActions>[0], viewer).more.flatMap(group => group.actions.map(a => a.id as string));
  // A client that says nothing keeps what the page offered before.
  assert.deepEqual(page({platform: 'web'}).filter(id => ['playNext', 'addToQueue', 'watchWithGroup', 'addToGroupQueue', 'download', 'delete'].includes(id)), ['download']);
  const full = page({platform: 'web', owner: true, queue: true, group: true, downloads: true, canDelete: true});
  for (const id of ['playNext', 'addToQueue', 'watchWithGroup', 'addToGroupQueue', 'download', 'delete']) assert.ok(full.includes(id), id);
  // Downloads switched off on the server, or nothing to download for this title: no Download.
  assert.equal(page({platform: 'web', downloads: false}).includes('download'), false);
  // A TV never offers Download or Delete.
  const tv = page({platform: 'tv', owner: true, downloads: true, canDelete: true});
  assert.equal(tv.includes('download') || tv.includes('delete'), false);
});
