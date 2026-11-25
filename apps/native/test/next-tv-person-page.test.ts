import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV person page (pages/tv/person/PersonPage.tsx) is TitleFrame filled from `usePerson` (its own test holds the data).
// This holds what the TV page adds (tv-detail frame 10): the departments over the name, place and count in the facts, a
// row per department with Filters on the first, the Filters menu, and where focus starts. Seen on the Android TV emulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})},
  useState: (initial: unknown) => [initial, () => {}], useRef: () => ({current: null}), useEffect: () => {}};

async function page(view: Record<string, unknown>, role = 'all') {
  const {PersonPage} = await screenModule(new URL('../src-next/pages/tv/person/PersonPage.tsx', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/controls': {Chip: 'Chip'},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt', PersonCircle: 'PersonCircle'},
    '../../../kit/frames': {TitleFrame: 'TitleFrame', PanelBlock: 'PanelBlock', PosterWindowRow: 'PosterWindowRow', StateRow: 'StateRow'},
    '../../../kit/layout': {HeroMetadata: 'HeroMetadata', PosterRow: 'PosterRow'},
    '../../shared/person/usePerson': {usePerson: () => view},
  });
  const node = PersonPage({role, focusScope: 'person:x', onBack: () => {}, runtime: {}}) as Node;
  const [frame, menu] = node.props.children as Node[];
  return {frame: frame!, menu: menu!, parts: (frame!.props.children.flat().filter(Boolean) as Node[])};
}

const choices = (selected: string, pressed: string[]) => (['all', 'cast', 'crew'] as const).map(id => ({id, label: id, selected: id === selected, onPress: () => pressed.push(id)}));
const view = (over: Record<string, unknown> = {}) => ({name: 'Steven Spielberg', initials: 'SS', mosaic: [], roles: 'Directing, Production', birthPlace: 'Cincinnati, Ohio',
  facts: [{id: 'roles', text: 'Directing, Production', role: 'identity'}, {id: 'dates', text: 'Born December 18, 1946', role: 'context'}],
  biography: {text: 'An American filmmaker.', readMore: {label: 'Read more', expanded: false, onPress: () => {}}},
  knownFor: {title: 'Known for', items: [{key: 'movie:jaws', title: 'Jaws'}]},
  credits: {title: 'Credits', count: '6 titles on this server', choices: choices('all', []), rows: [{id: 'dir', title: 'Directing', count: '5 titles'}, {id: 'prod', title: 'Production', count: '3 titles'}]},
  ...over});

test('f9: the departments over the name, the dates, place and count in the facts, and a row per department with Filters on the first', async () => {
  const {frame, parts} = await page(view());
  assert.deepEqual(frame.props.kicker, {label: 'Directing, Production'});
  assert.deepEqual(((frame.props.metadata as Node).props.facts as {text: string}[]).map(fact => fact.text), ['Born December 18, 1946', 'Cincinnati, Ohio', '6 titles on this server']);
  assert.deepEqual(parts.map(part => part.type), ['PosterRow', 'PosterWindowRow', 'PosterWindowRow']);
  assert.equal((parts[1]!.props.tools as Node).props.testID, 'person-role-filter', 'Filters sits on the first department');
  assert.equal(parts[2]!.props.tools, undefined);
});

test('Filters opens All, Cast and Crew, each choosing its read', async () => {
  const pressed: string[] = [];
  const {menu} = await page(view({credits: {...view().credits, choices: [{id: 'select', label: 'Select', onPress: () => {}}, ...choices('cast', pressed)]}}));
  const items = menu.props.items as {id: string; selected: boolean; onSelect: () => void}[];
  assert.deepEqual(items.map(item => [item.id, item.selected]), [['all', false], ['cast', true], ['crew', false]], 'Select is a touch control');
  items[2]!.onSelect(); assert.deepEqual(pressed, ['crew']);
  assert.equal(menu.props.title, t('next.browse.native.filters'));
});

test('focus starts on Read more, else the first title they are known for, and after a filter back on Filters', async () => {
  assert.equal((await page(view())).frame.props.startFocus, 'title-read-more');
  assert.equal((await page(view({biography: undefined}))).frame.props.startFocus, 'movie:jaws');
  assert.equal((await page(view(), 'cast')).frame.props.startFocus, 'person-role-filter');
});
