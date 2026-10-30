import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {channelStatus, previewMatches, previewDuration} from '../src-next/screens/settings-live/server/channels/presentation.ts';
import {storageExtraSummary} from '../src-next/screens/settings-live/server/storage/summary.ts';
import {maintenance} from './helpers/owner-server-settings.mjs';

const t = defaultI18n.t;
test('channel states use the five approved labels and never present a problem as on air', () => {
  assert.deepEqual(channelStatus({state: 'healthy', healthCode: ''}), {label: 'server.channels.state.ready', tone: 'healthy'});
  assert.deepEqual(channelStatus({state: 'generating', healthCode: ''}), {label: 'server.channels.state.generating', tone: 'building'});
  assert.deepEqual(channelStatus({state: 'stale', healthCode: ''}), {label: 'server.channels.state.stale', tone: 'warning'});
  assert.deepEqual(channelStatus({state: 'disabled', healthCode: ''}), {label: 'server.channels.state.disabled', tone: 'neutral'});
  for (const state of ['no-eligible-media', 'unresolved-duration', 'new-server-token', '']) assert.deepEqual(channelStatus({state, healthCode: ''}), {label: 'server.channels.state.degraded', tone: 'danger'}, state);
  assert.deepEqual(channelStatus({state: 'healthy', healthCode: 'too_few_titles'}), {label: 'server.channels.state.degraded', tone: 'danger'});
});

test('budget-limited counts and total duration stay lower bounds, including a partially counted rule', () => {
  assert.equal(previewMatches(2, true, t), '2 titles match');
  assert.equal(previewMatches(2, false, t), 'At least 2 titles match');
  assert.equal(previewMatches(1, false, t), 'At least 1 title matches');
  const result = {durationMs: 5400000, complete: true, rules: [{complete: true}]} as any;
  assert.equal(previewDuration(result), '≈ 1.5 h');
  assert.equal(previewDuration({...result, complete: false}), '≥ 1.5 h');
  assert.equal(previewDuration({...result, rules: [{complete: false}]}), '≥ 1.5 h');
});

test('expert retention summaries include every category even when producers lack published defaults', () => {
  const runtime = {jobDays: 9} as any, settings = {...maintenance(), backgroundSpeed: 'normal' as const, retention: {extra: 45}}, doc = {storageCategories: [{id: 'extra', name: 'Extra files', retentionKey: 'extra'}, {id: 'conversion', name: 'Conversions', retentionKey: 'conversions'}]} as any;
  const logs = {retention: [{category: 'server', days: 18}, {category: 'playback', days: 4}]} as any;
  const summary = storageExtraSummary(runtime, settings, doc, logs, t).join(' ');
  assert.match(summary, /9 days/); assert.match(summary, /Extra files: 45 days/); assert.match(summary, /18 days/); assert.match(summary, /4 days/);
  assert.doesNotMatch(summary, /Conversions|All at defaults/);
  assert.ok(storageExtraSummary(runtime, {...settings, retention: {extra: 0}}, doc, logs, t).includes('Extra files: Never'));
  assert.deepEqual(storageExtraSummary(undefined, undefined, undefined, undefined, t), []);
});

test('missing rows distinguish completed removals and calendar policy dates without following unsaved settings', async () => {
  const {screenModule} = await import('./helpers/screen-harness.mjs');
  const {formsFixture, lifecycleHooks, nodes, content, specification, theme, bindingModule} = await import('./helpers/owner-server-settings.mjs');
  const values = await import('../src-next/screens/settings-live/server/settings-values.ts');
  const bindings = await bindingModule();
  const fixture = await formsFixture({runtime: {'library.missingTitles': 'after_days', 'library.missingTitlesDays': 90}});
  const outer = lifecycleHooks(), child = lifecycleHooks();
  let activeHost = outer;
  const react: any = Object.fromEntries(Object.keys(outer.react).filter(key => key !== 'default').map(key => [key, (...args: any[]) => (activeHost.react as Record<string, any>)[key](...args)]));
  react.default = react;
  const page = {total: 2, rule: 'after_days', days: 30, items: [
    {id: 'removed', title: 'Removed title', missingSince: '2026-10-01T12:00:00Z', removedAt: '2026-10-05T12:00:00Z'},
    {id: 'waiting', title: 'Waiting title', missingSince: '2026-10-01T12:00:00Z'},
  ]};
  const state = {phase: 'ready', page, busy: false};
  class Source {subscribe = () => () => {}; getSnapshot = () => state; load = async () => {}; dispose() {}}
  class Lifetime {current = fixture.group.context.current; dispose() {}}
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/MissingTitlesTab.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/client-core/src/server-admin/missing-source': {SettingsMissingSource: Source},
    '../../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../../packages/client-core/src/server-admin/console-words': {when: () => 'relative time'},
    '../../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../../kit/overlays': {BottomSheet: 'BottomSheet'},
    '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme},
    '../../lifetime': {NativeScreenLifetime: Lifetime}, '../AdvancedSection': {NativeAdvancedSection: 'Advanced'},
    '../SettingControl': {NativeSettingControl: 'Setting'}, '../settings-values': values,
    './space-source': {nativeStorageOwner: () => true}, './forms': fixture.hooks,
  });
  const props = {ports: {context: fixture.group.context}, runtime: {reportError() {}}, t};
  outer.render(() => module.MissingTitlesTab(props)); outer.flush();
  const element = outer.render(() => module.MissingTitlesTab(props));
  activeHost = child;
  const render = () => child.render(() => element.type(element.props));
  const text = () => nodes(render()).filter((node: any) => node.type === 'Text').map(content);
  assert.ok(text().includes(`Removed ${defaultI18n.date(Date.parse('2026-10-05T12:00:00Z'))}`));
  assert.ok(text().includes(t('web.missingTitles.sinceRemoved', {when: 'relative time', removes: defaultI18n.date(Date.parse('2026-10-31T12:00:00Z'))})));
  (fixture.group.forms as Record<string, {set: (update: (draft: any) => any) => void}>).runtime!.set((draft: any) => ({...draft, 'library.missingTitlesDays': 7}));
  assert.ok(text().includes(t('web.missingTitles.sinceRemoved', {when: 'relative time', removes: defaultI18n.date(Date.parse('2026-10-31T12:00:00Z'))})));
  child.dispose(); outer.dispose(); fixture.stop();
});
