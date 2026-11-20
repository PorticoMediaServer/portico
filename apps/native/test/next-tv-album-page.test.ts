import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV album page (pages/tv/music/AlbumPage.tsx) is TitleFrame filled from `useAlbum` (its own test holds the data). This
// holds what the TV page adds (tv-more frame 6): the cover at the left, "Album", the artist, Play / Shuffle / heart / Add to
// playlist / ⋯, the songs beside the cover with a multi-disc album's Disc choices, More like this, and W-12. Seen on the
// Android TV emulator and the Apple TV simulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})},
  useState: (initial: unknown) => [typeof initial === 'function' ? (initial as () => unknown)() : initial, () => {}]};

let current: Record<string, unknown> = {}, given: {openEntry?: (entry: unknown) => void} = {};
const load = (async () => screenModule(new URL('../src-next/pages/tv/music/AlbumPage.tsx', import.meta.url), {
  react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork'},
  '../../../kit/frames': {ChoiceChips: 'ChoiceChips', PanelBlock: 'PanelBlock', StateRow: 'StateRow', TitleFrame: 'TitleFrame'},
  '../../../kit/layout': {HeroMetadata: 'HeroMetadata', PosterRow: 'PosterRow'}, '../../../kit/music/MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
  '../../../kit/music/MusicSectionControls': {MusicSectionControls: 'MusicSectionControls'}, '../../../kit/music/TrackWindow': {TrackWindow: 'TrackWindow'},
  '../../shared/music/useOpenedReturn': await screenModule(new URL('../src-next/pages/shared/music/useOpenedReturn.ts', import.meta.url), {react: {...react, ...react.default}}),
  '../../shared/music/useAlbum': {useAlbum: (props: typeof given) => {given = props; return current;}},
}))();
const songs = {rows: [], select: () => undefined, onPlay: () => {}, onMore: () => {}, onFocusRow: () => {}, onBlurRow: () => {}, section: {}, next: () => {}, previous: () => {}, retry: () => {}, refresh: () => {}};
const view = (over: Record<string, unknown> = {}) => ({title: 'DAMN.', facts: [], badges: [], byline: {text: 'Kendrick Lamar', onPress: () => {}}, play: () => {}, shuffle: () => {}, more: () => {},
  favorite: {on: false, toggle: () => {}}, songs, ...over});
const entry = {id: 'damn', libraryId: 'music', kind: 'album', title: 'DAMN.'};
async function page(over: Record<string, unknown> = {}, props: Record<string, unknown> = {}, openEntry: (value: unknown) => void = () => {}) {
  current = view(over);
  const {AlbumPage} = await load;
  const frame = AlbumPage({entry, openEntry, onBack: () => {}, ...props}) as Node;
  return {frame, parts: frame.props.children.flat().filter(Boolean) as Node[], beside: ((frame.props.beside as Node).props.children as unknown[]).flat().filter(Boolean) as Node[]};
}

test('frame 6: the square cover, "Album" over the title, the artist with their initials, Play / Shuffle / heart / Add to playlist / ⋯', async () => {
  const {frame} = await page({}, {onPlaylist: () => {}});
  assert.equal((frame.props.cover as {shape: string}).shape, 'square');
  assert.equal((frame.props.kicker as {label: string}).label, t('search.kind.album'));
  assert.deepEqual([(frame.props.byline as {text: string}).text, (frame.props.byline as {initials: string}).initials], ['Kendrick Lamar', 'K']);
  assert.deepEqual((frame.props.actions as {testID: string}[]).map(action => action.testID), ['album-shuffle', 'album-favorite', 'album-playlist', 'album-menu']);
  assert.equal(frame.props.startFocus, undefined, 'a fresh visit starts on Play');
  const without = await page();
  assert.equal((without.frame.props.actions as {testID: string}[]).some(action => action.testID === 'album-playlist'), false, 'no playlists: no button');
});

test('the songs beside the cover, paged by the server; a multi-disc album\'s Disc choices over them; More like this under', async () => {
  const one = await page({similar: {testID: 'music-similar-damn', items: []}});
  assert.deepEqual(one.beside.map(node => node.type), ['TrackWindow', 'MusicSectionControls']);
  assert.deepEqual(one.parts.map(node => node.type), ['PosterRow']);
  assert.equal(one.parts[0]!.props.focusKey, `music-similar-damn.${JSON.stringify(['music', 'damn'])}`);
  const discs = [{id: '1', label: 'Disc 1', selected: true, onPress: () => {}}];
  assert.deepEqual((await page({discs})).beside.map(node => node.type), ['ChoiceChips', 'TrackWindow', 'MusicSectionControls']);
  const failed = await page({state: {kind: 'error'}});
  assert.deepEqual(failed.beside.map(node => node.type), ['TrackWindow'], 'one failure state: the page\'s Try again');
});

test('W-12: Back to the album starts on the album card it opened from More like this, once', async () => {
  await page({}, {}, () => {});
  given.openEntry!({id: 'thriller', kind: 'album'});
  const back = await page({similar: {testID: 'music-similar-damn', items: [{key: 'thriller'}]}});
  assert.equal(back.frame.props.startFocus, 'thriller');
  assert.equal((await page()).frame.props.startFocus, undefined, 'used once');
});
