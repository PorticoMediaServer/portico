import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV film page (pages/tv/title/FilmPage.tsx) is TitleFrame filled from `useTitle` (its own test holds the data).
// This holds what the TV page adds: no Download on TV, Details with the synopsis, the tagline and the cast's note
// (detail-tv-4), and each section named by its tab so the folded tabs follow focus. Seen on the Android TV emulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})}};

async function film(view: Record<string, unknown>) {
  let given: Record<string, unknown> | undefined;
  const kit = {TitleFrame: 'TitleFrame', TitleSection: 'TitleSection', TitleDetails: 'TitleDetails'};
  const {FilmPage} = await screenModule(new URL('../src-next/pages/tv/title/FilmPage.tsx', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/artwork': {AmbientArt: 'AmbientArt'},
    '../../../kit/frames': kit, '../../../kit/layout': {HeroMetadata: 'HeroMetadata', PosterRow: 'PosterRow'},
    '../../shared/title/useTitle': {useTitle: (props: Record<string, unknown>) => {given = props; return view;}},
  });
  const props = {back: () => {}, download: () => {}, aboutLabel: 'About'};
  return {node: FilmPage(props) as Node | null, given: given!};
}

const view = {retired: false, entry: {id: 'f1'}, title: 'Dune: Part Two', art: {}, facts: [], credits: [],
  actions: [{icon: 'i-plus', label: 'Add to My List', testID: 'watch-title-my-list', labelled: true, onPress: () => {}}, {icon: 'i-more', label: 'More', onPress: () => {}}],
  synopsis: 'Paul unites with Chani.', tagline: 'Long live the fighters.',
  tabs: {value: 'related', items: [], onValueChange: () => {}},
  related: [{title: 'More like this', testID: 'watch-related-r1', items: []}, {title: 'Starring Zendaya', testID: 'watch-related-r2', items: []}],
  details: {about: {title: 'About', rows: [{label: 'Released', values: [{text: 'March 1, 2024'}]}]},
    people: {title: 'Cast & crew', groups: [[{id: 'c1', name: 'Zendaya'}]], lines: [], seeAll: {label: 'See all', onPress: () => {}}}},
  people: {groups: [[{id: 'c1', name: 'Zendaya'}, {id: 'c2', name: 'Javier Bardem'}]], more: {label: 'Show more', loading: false, onPress: () => {}}}};

test('a TV film page offers no Download, and its sections are the related rows then Details, each named by its tab', async () => {
  const {node, given} = await film(view);
  assert.equal(given.download, undefined, 'a TV keeps nothing for offline');
  assert.equal(node!.type, 'TitleFrame');
  assert.deepEqual((node!.props.actions as {label: string}[]).map(action => action.label), [t('next.controls.myList'), 'More'], 'tv-detail 1: My List');
  const sections = node!.props.children.flat().filter(Boolean) as Node[];
  assert.deepEqual(sections.map(section => section.props.tab), ['related', 'related', 'details']);
  assert.deepEqual(sections.slice(0, 2).map(section => (section.props.children[0] as Node).props.title), ['More like this', 'Starring Zendaya']);
  const details = sections[2]!.props.children[0] as Node;
  assert.equal(details.type, 'TitleDetails');
  assert.deepEqual(details.props.about, {title: 'About', rows: view.details.about.rows, body: 'Paul unites with Chani.', note: 'Long live the fighters.'});
  const people = details.props.people as {note: string; seeAll?: unknown; groups: unknown; more?: unknown};
  assert.equal(people.note, t('next.detail.castThenCrew'));
  // detail-tv-4 draws no See all: the TV row holds everyone read so far and reads the next page as focus reaches its end.
  assert.equal(people.seeAll, undefined);
  assert.equal(people.groups, view.people.groups);
  assert.equal(people.more, view.people.more.onPress);
  const busy = (await film({...view, people: {...view.people, more: {...view.people.more, loading: true}}})).node!;
  const busyDetails = ((busy.props.children.flat().filter(Boolean).at(-1) as Node).props.children[0] as Node).props.people as {more?: unknown};
  assert.equal(busyDetails.more, undefined, 'one page at a time');
});

test('a film with no facts still shows its synopsis in About; a retired page draws nothing', async () => {
  const bare = (await film({...view, details: {}})).node!;
  const about = ((bare.props.children.flat().filter(Boolean).at(-1) as Node).props.children[0] as Node).props.about;
  assert.deepEqual(about, {title: 'About', rows: [], body: 'Paul unites with Chani.', note: 'Long live the fighters.'});
  assert.equal((await film({...view, retired: true})).node, null);
});
