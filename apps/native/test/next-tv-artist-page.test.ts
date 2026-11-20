import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV artist page (pages/tv/music/ArtistPage.tsx) is TitleFrame filled from `useArtist` (its own test holds the data).
// This holds what the TV page adds (tv-more frame 8): the circle at the right, Play / Shuffle / the heart / ⋯, Top songs with
// the Albums column and Details beside them, the other releases, similar artists and All songs; and W-12, Back to the page
// starting on the album it opened. Seen on the Android TV emulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})},
  useState: (initial: unknown) => [typeof initial === 'function' ? (initial as () => unknown)() : initial, () => {}]};

let current: Record<string, unknown> = {}, given: {openEntry?: (entry: unknown) => void} = {};
const load = (async () => screenModule(new URL('../src-next/pages/tv/music/ArtistPage.tsx', import.meta.url), {
  react, '../../../../../../packages/i18n/src': {defaultI18n},
  '../../../kit/artwork': {AmbientArt: 'AmbientArt'}, '../../../kit/controls': {Button: 'Button'},
  '../../../kit/frames': {AboutNote: 'AboutNote', AboutTable: 'AboutTable', ArtistCover: 'ArtistCover', AsideSection: 'AsideSection', PanelBlock: 'PanelBlock', StateRow: 'StateRow', TitleFrame: 'TitleFrame'},
  '../../shared/music/useOpenedReturn': await screenModule(new URL('../src-next/pages/shared/music/useOpenedReturn.ts', import.meta.url), {react: react.default ? {...react, ...react.default} : react}),
  '../../../kit/layout': {HeroMetadata: 'HeroMetadata', PosterColumn: 'PosterColumn', PosterRow: 'PosterRow'},
  '../../../kit/music/MusicSectionControls': {MusicSectionControls: 'MusicSectionControls'}, '../../../kit/music/TrackWindow': {TrackWindow: 'TrackWindow'},
  '../../shared/music/useArtist': {useArtist: (props: typeof given) => {given = props; return current;}},
}))();
const songs = {rows: [], select: () => undefined, onPlay: () => {}, onMore: () => {}};
const view = (over: Record<string, unknown> = {}) => ({title: 'The Beatles', initials: 'T', covers: [], facts: [], factsTV: [], play: () => {}, shuffle: () => {}, more: () => {},
  favorite: {on: false, toggle: () => {}}, details: {rows: [], overview: 'A band.', source: 'From Wikipedia'},
  topSongs: {title: 'Top songs', ...songs},
  releases: [{group: 'albums', title: 'Albums', testID: 'music-releases-albums', items: [{key: 'abbey', title: 'Abbey Road'}]},
    {group: 'singles', title: 'Singles & EPs', testID: 'music-releases-singles', items: [{key: 'single', title: 'Something'}]}],
  releasePages: {section: {phase: 'ready'}}, allSongs: {loading: false, load: () => {}, section: {}}, ...over});
const entry = {id: 'beatles', libraryId: 'music', kind: 'artist', title: 'The Beatles'};
async function page(over: Record<string, unknown> = {}, openEntry: (value: unknown) => void = () => {}) {
  current = view(over);
  const {ArtistPage} = await load;
  const frame = ArtistPage({entry, openEntry, onBack: () => {}}) as Node;
  return {frame, parts: frame.props.children.flat().filter(Boolean) as Node[], aside: ((frame.props.aside as Node).props.children as unknown[]).flat().filter(Boolean) as Node[]};
}

test('frame 8: "Artist" over the name, the long facts line, Play / Shuffle / heart / ⋯, the circle at the right with its rings', async () => {
  const {frame} = await page();
  assert.equal((frame.props.kicker as {label: string}).label, t('search.kind.artist'));
  assert.equal((frame.props.primary as {testID: string}).testID, 'artist-play');
  assert.deepEqual((frame.props.actions as {testID: string}[]).map(action => action.testID), ['artist-shuffle', 'artist-favorite', 'artist-menu']);
  const cover = frame.props.cover as {shape: string; large: boolean; node: (size: number) => Node};
  assert.deepEqual([cover.shape, cover.large], ['circle', true], 'the frame puts a large TV circle at the right');
  assert.equal(cover.node(360).type, 'ArtistCover', 'the kit draws the circle and its rings (design: 40 and 80 out on TV)');
  assert.equal(frame.props.startFocus, undefined, 'a fresh visit starts on Play');
});

test('the heart says and toggles whether this viewer favourites the artist', async () => {
  let toggled = 0;
  const {frame} = await page({favorite: {on: true, toggle: () => {toggled += 1;}}});
  const heart = (frame.props.actions as {testID: string; icon: string; active: boolean; label: string; onPress: () => void}[])[1]!;
  assert.deepEqual([heart.icon, heart.active, heart.label], ['i-heart-f', true, t('entry.removeFromFavorites')]);
  heart.onPress(); assert.equal(toggled, 1);
});

test('Top songs, then the other releases, their paging, similar artists and All songs; Albums and Details beside them', async () => {
  const {parts, aside} = await page({similar: {testID: 'similar'}});
  assert.deepEqual(parts.map(part => part.type), ['TrackWindow', 'PosterRow', 'PanelBlock', 'PosterRow', 'PanelBlock']);
  assert.equal(parts[1]!.props.testID, 'music-releases-singles', 'Albums is not repeated as a row');
  assert.equal(((parts[4]!.props.children as Node[])[0]!).props.testID, 'music-all-songs', 'All songs is read when asked for');
  assert.deepEqual(aside.map(node => node.type), ['AsideSection', 'AsideSection']);
  const column = [aside[0]!.props.children].flat(2).find((node: any) => node?.type === 'PosterColumn') as Node;
  assert.deepEqual((column.props.items as {key: string}[]).map(item => item.key), ['abbey']);
  // W-12 on tvOS: the page drawn again on Back has the same region names, so the layer stack finds the card that opened it.
  assert.equal(column.props.focusKey, `music-releases-albums.${JSON.stringify(['music', 'beatles'])}`);
  assert.equal(parts[1]!.props.focusKey, `music-releases-singles.${JSON.stringify(['music', 'beatles'])}`);
  const note = [aside[1]!.props.children].flat(2).find((node: any) => node?.type === 'AboutNote') as Node;
  assert.deepEqual([note.props.text, note.props.source], ['A band.', 'From Wikipedia'], 'the overview and where it is from');
});

test('All songs once read: their rows, paged by the server, the focused row kept', async () => {
  const focus = () => {};
  const {parts} = await page({allSongs: {list: songs, total: '40 songs', loading: false, section: {}, focus, next: () => {}}});
  const all = parts.filter(part => part.type === 'TrackWindow')[1]!;
  assert.deepEqual(all.props.heading, {title: t('music.allSongs'), trailing: '40 songs'});
  assert.equal(all.props.onFocusRow, focus);
  assert.equal(parts.at(-1)!.type, 'PanelBlock');
});

test('W-12: Back to the artist starts on the album card it opened, once; a later visit starts on Play', async () => {
  const opened: unknown[] = [];
  await page({}, value => opened.push(value));
  given.openEntry!({id: 'abbey', kind: 'album'});
  assert.equal(opened.length, 1);
  const back = await page({releasePages: {section: {phase: 'loading'}}, releases: []});
  assert.equal(back.frame.props.startFocus, 'abbey', 'while the releases load, the card is the start');
  const later = await page();
  assert.equal(later.frame.props.startFocus, undefined, 'the return is used once');
  await page(); given.openEntry!({id: 'abbey', kind: 'album'});
  const gone = await page({releases: []});
  assert.equal(gone.frame.props.startFocus, undefined, 'the album is gone once the releases are read: Play');
  await page(); given.openEntry!({id: 'other', kind: 'artist'});
  assert.equal((await page()).frame.props.startFocus, undefined, 'a similar artist opened is not a return');
});
