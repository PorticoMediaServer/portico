import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {tree} from './helpers/interaction-harness.mjs';

async function render(phase: string, rows: unknown[] = []) {
  const host = hooks(), data = {item: {id: 'film', kind: 'movie', title: 'Film'}, scope: {itemId: 'film'}}, view = {tab: 'related'};
  const store = (value: unknown) => ({subscribe: () => () => {}, getSnapshot: () => value});
  let retries = 0;
  const binding = {service: store({phase: 'ready', data}), view: store(view), recommendations: store({phase, ...(phase === 'ready' ? {data: {rows}} : {})}),
    context: {preferences: store({}), effects: {}}, readRecommendations: () => {retries++;}};
  const {TitleBody} = await componentModule(new URL('../src/screens/detail/Detail.tsx', import.meta.url), {
    react: {...host.react, useId: () => 'panel'},
    './text': {t: (key: string) => key}, './watch-body-playback': {useWatchBodyPlayback: () => undefined},
    '@core/preferences.ts': {preferenceValue: () => true}, '../../shell/viewport': {useShellWidth: () => 1440},
    '../../kit/controls': {Tabs: 'Tabs'}, './Loading': {DetailLoading: 'DetailLoading'}, './detail.module.css': {default: {}},
  });
  const nodes = tree(host.render(() => TitleBody({binding})));
  // A film's page is the title frame: the screen hands FilmTitle the tabs, the chosen tab and the panel (its children).
  const film = nodes.find((node: any) => node.type?.name === 'FilmTitle');
  return {tabs: {value: film.props.tab, items: film.props.tabs}, panel: film, retryCount: () => retries};
}

test('a film with no recommendation rows opens Details and omits the empty tab', async () => {
  const {tabs, panel} = await render('ready');
  assert.equal(tabs.value, 'details'); assert.deepEqual(tabs.items.map((item: any) => item.value), ['details']);
  assert.ok(tree(panel).some((node: any) => node.type?.name === 'TitleDetails'));
});

test('a populated row or an empty page with a real continuation keeps More like this', async () => {
  for (const row of [{id: 'row', entries: [{id: 'other'}]}, {id: 'row', entries: [], nextCursor: 'opaque'}]) {
    const {tabs} = await render('ready', [row]);
    assert.equal(tabs.value, 'related'); assert.ok(tabs.items.some((item: any) => item.value === 'related'));
  }
});

test('a pending or failed recommendations read shows loading or Retry rather than claiming an empty source', async () => {
  for (const phase of ['idle', 'loading', 'error']) {
    const {tabs, panel, retryCount} = await render(phase);
    assert.equal(tabs.value, 'related');
    if (phase === 'error') {
      const failure = tree(panel).find((node: any) => node.type?.name === 'ScreenFailure');
      assert.ok(failure); failure.props.retry(); assert.equal(retryCount(), 1);
    } else assert.ok(tree(panel).some((node: any) => node.type === 'DetailLoading'));
  }
});
