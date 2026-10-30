/** Search hero callbacks, using the actual component with isolated service snapshots. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {runScreenCommand} from '../src/screens/detail/context.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, tree} from './helpers/interaction-harness.mjs';
const t = (key: any, values?: any) => defaultI18n.t(key, values);
async function mount(options: {kind?: 'movie' | 'show'; phase?: string; selectedId?: string; actions?: any[]; busy?: boolean} = {}) {
  const h = createRenderer(), calls: {name: string; args: any[]}[] = [];
  const record = (name: string) => (...args: any[]) => { calls.push({name, args}); return Promise.resolve(); };
  const entry = {id: 'selected', libraryId: 'library', kind: options.kind ?? 'show', title: 'Result', displayTitle: 'Stored title'};
  const series = {show: {id: options.selectedId ?? entry.id, year: 2026, genres: []}, seasonTotalCount: 2};
  const movie = {item: {id: options.selectedId ?? entry.id, year: 2026, duration: 60}, metadata: {genres: []}};
  const primary = {actions: options.actions ?? [{id: 'resume', enabled: true, playback: {}}]};
  const service = (snapshot: any) => ({subscribe: () => () => {}, getSnapshot: () => snapshot, cancel() {}});
  const title = {service: service({phase: options.phase ?? 'ready', data: movie}), select: record('selectTitle')};
  const show = {service: service({phase: options.phase ?? 'ready', data: series}), select: record('selectShow'), current: record('current'),
    primary: {service: service({data: primary}), play: record('play')},
    personal: {...service({watchlisted: false}), getBusy: () => options.busy === true}, setMyList: record('myList')};
  const binding = {topTitle: title, topShow: show, open: record('open'), context: {effects: {reportError: record('error')},
    preferences: service({effective: {'appearance.showBackdrops': false}}), artwork: {ambient() {}, foreground() {}}}};
  const app = await componentModule(new URL('../src/screens/search/TopResult.tsx', import.meta.url), {
    react: h.react, '../detail/model': {titlePageFacts: () => []},
    '../../kit/artwork': {AmbientArt: 'AmbientArt'}, '../../kit/controls': {Button: 'Button'},
    '../../kit/title/TitleParts': {SearchResultHero: 'SearchResultHero', TitleFactsLine: 'TitleFactsLine'},
    '../detail/Detail': {TitleActions: 'TitleActions'}, '../detail/text': {t, environment: {t, locale: 'en-US'}},
    '../detail/detail.module.css': {default: {}}, '../detail/context': {runScreenCommand},
  });
  const flat = () => tree(h.render(() => app.TopResult({binding, result: {entry, group: entry.kind === 'show' ? 'shows' : 'movies'}})));
  return {calls, flat, entry, series, movie, primary, title};
}

test('ready show Play and My List use the shown series and enabled primary action', async () => {
  const f = await mount();
  const buttons = all(f.flat(), 'Button');
  buttons.find(button => button.props.variant === 'primary').props.onClick();
  assert.equal(f.calls.find(call => call.name === 'current')?.args[0], f.series);
  assert.deepEqual(f.calls.find(call => call.name === 'play')?.args, [f.primary, 'resume']);
  const list = buttons.find(button => button.props['aria-pressed'] === false); assert.ok(list);
  list.props.onClick();
  assert.deepEqual(f.calls.find(call => call.name === 'myList')?.args, [f.series, true]);
});

test('disabled, non-playback and Start over actions do not become the hero Play button', async () => {
  const f = await mount({actions: [{id: 'disabled', enabled: false, playback: {}}, {id: 'details', enabled: true}, {id: 'start_over', enabled: true, playback: {}}], busy: true});
  const buttons = all(f.flat(), 'Button');
  assert.equal(buttons.some(button => button.props.variant === 'primary'), false);
  assert.equal(buttons.find(button => button.props['aria-pressed'] === false)?.props.disabled, true);
});

test('loading or mismatched detail data keeps Details bound to the selected search entry', async () => {
  for (const options of [{phase: 'loading'}, {selectedId: 'previous-result'}]) {
    const f = await mount(options);
    one(f.flat(), 'Button').props.onClick();
    assert.deepEqual(f.calls.find(call => call.name === 'open')?.args, [f.entry]);
    assert.equal(f.calls.some(call => ['play', 'myList'].includes(call.name)), false);
  }
});

test('ready movie forwards its exact title binding and document to TitleActions', async () => {
  const f = await mount({kind: 'movie'});
  const actions = one(f.flat(), 'TitleActions');
  assert.equal(actions.props.binding, f.title);
  assert.equal(actions.props.data, f.movie);
  assert.equal(actions.props.topResult, true);
});
