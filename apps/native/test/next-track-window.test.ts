import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// `TrackWindow` (kit/music) draws a music page's songs as the kit's track rows, a window at a time. This holds what it adds
// to the rows: each row's place in the page's selection (a song's menu offers Select; a selected tap toggles instead of
// playing), the focused row reported to the page, and the quiet heading drawn with the rows. The rows' own drawing and
// gestures are TrackRow's and CardFrame's (next-a11y-kit-render); seen on the Pixel on the album and artist pages.

type Node = Readonly<{type: unknown; props: Record<string, any>}>;
const react = {default: {createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})}};
const nodes = (value: any): Node[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];

async function window(props: Record<string, unknown>) {
  const {TrackWindow} = await screenModule(new URL('../src-next/kit/music/TrackWindow.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'}, '../browse': {TrackRow: 'TrackRow', TrackHeader: 'TrackHeader', trackColumns: {album: ['number', 'title', 'plays', 'duration'], 'top-songs': ['number', 'title', 'plays', 'duration'], chapters: ['number', 'title', 'duration']}, trackRowHeight: (_theme: unknown, _variant: string, current?: boolean) => current ? 76 : 54},
    '../frames/title-parts': {PanelBlock: 'PanelBlock', PanelHeading: 'PanelHeading'}, '../selection': {SelectionCircle: 'SelectionCircle'},
    '../theme': {useTheme: () => ({platform: 'phone', fontScale: 1, textScale: 1})}, './MusicWindow': {MusicWindow: 'MusicWindow'}});
  const tree = TrackWindow(props) as Node;
  const list = nodes(tree).find(node => node.type === 'MusicWindow')!;
  return {tree, list, row: (index: number) => nodes(list.props.renderItem(list.props.items[index], index)).find(node => node.type === 'TrackRow')!,
    host: (index: number) => list.props.renderItem(list.props.items[index], index) as Node};
}

const rows = [{id: 's1', text: {title: 'Alone Again'}}, {id: 's2', text: {title: 'Too Late'}}];

test('a row\'s place in the selection: Select from its menu enters it, and once selecting a tap toggles the song', async () => {
  const calls: string[] = [];
  let selecting = false, selected = false;
  const select = (id: string) => ({active: selecting, selected: id === 's2' && selected, onEnter: () => {calls.push(`enter ${id}`); selecting = true;}, onToggle: () => {calls.push(`toggle ${id}`); selected = !selected;}});
  const resting = await window({rows, variant: 'album', select, onPlay: (id: string) => calls.push(`play ${id}`), onMore: (id: string) => calls.push(`more ${id}`)});
  const row = resting.row(1);
  assert.equal(row.props.selection.active, false);
  row.props.selection.onEnter('s2'); assert.deepEqual(calls, ['enter s2']);
  const active = await window({rows, variant: 'album', select});
  const now = active.row(1).props.selection;
  assert.equal(now.active, true);
  assert.equal(now.isSelected('s2'), false);
  now.onToggle('s2'); assert.equal(now.isSelected('s2'), true, 'a row reads the page\'s selection as it is now');
  assert.deepEqual(calls, ['enter s2', 'toggle s2']);
  assert.ok(now.renderControl(rows[1], true), 'a selecting row draws its circle');
});

test('the focused row is reported to the page, a heading is drawn over the rows as one section, and the current chapter is taller', async () => {
  const calls: string[] = [];
  const focus = await window({rows, variant: 'top-songs', onPlay() {}, onFocusRow: (id: string) => calls.push(`focus ${id}`), onBlurRow: (id: string) => calls.push(`blur ${id}`)});
  const host = focus.host(0);
  host.props.onFocus(); host.props.onBlur();
  assert.deepEqual(calls, ['focus s1', 'blur s1']);
  assert.equal(focus.list.props.itemHeight, 54);
  assert.equal(nodes(focus.tree).some(node => node.type === 'PanelHeading'), false, 'no title, no heading');
  const titled = await window({rows, variant: 'top-songs', heading: {title: 'Top songs', quiet: true}, onPlay() {}});
  const heading = nodes(titled.tree).find(node => node.type === 'PanelHeading')!;
  assert.deepEqual([heading.props.title, heading.props.quiet], ['Top songs', true]);
  // A book's chapters: a section heading with its count at the end (collections 41a), the chapter you are in drawn taller.
  const chapters = await window({rows: [{...rows[0]}, {...rows[1], current: true}], variant: 'chapters', heading: {title: 'Chapters', trailing: '17'}, onPlay() {}});
  const loud = nodes(chapters.tree).find(node => node.type === 'PanelHeading')!;
  assert.deepEqual([loud.props.quiet, loud.props.trailing], [undefined, '17']);
  assert.equal(chapters.list.props.expanded.index, 1);
});
