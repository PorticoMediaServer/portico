import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as focus from '../src-next/kit/focus/registry';
import {settingsSpec} from '../../../packages/client-core/src/presentation/settings-spec';
import {tvGroupEdges, tvProgrammeGroups, tvSettingsResultGroups} from '../src-next/screens/settings-live/tv-settings-focus';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const theme = {isTV: true, colors: {text: 'primary', textSecondary: 'secondary', textTertiary: 'tertiary', accent: '#123456', accentStrong: 'accent'}};

async function focusFixture() {
  const registry = focus.createFocusRegistry(), host = hooks(), contexts: any[] = [];
  const react: any = {...host.react, useLayoutEffect() {}, createContext: (value: any) => {const context = {value}; contexts.push(context); return context;}, forwardRef: (render: any) => render};
  react.default = react;
  const module = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', TVFocusGuideView: 'Guide', findNodeHandle: (value: any) => value.tag},
    '../theme': {useTheme: () => theme}, './registry': focus,
    './CoveredLayer': {useLayerCovered: () => false}, './useHostRef': {useHostRef: () => ({native: {current: null}, ref() {}})},
  });
  contexts[0].value = registry;
  let tag = 1;
  const region = (id: string, children: string[], extra: any = {}) => {
    registry.configure({id, ...extra}); const guide = {tag: tag++, requestTVFocus() {}};
    registry.attachRegion(id, guide);
    for (const childId of children) registry.attach({regionId: id, childId}, {requestTVFocus() {}});
    return guide.tag;
  };
  return {registry, region, target(regionId: string, childId: string) {contexts[1].value = regionId; return host.render(() => module.useFocusTarget({focusId: childId}));}};
}

test('settings result crossings skip empty and disabled groups, reach Search above, and follow current attachment', async () => {
  const f = await focusFixture();
  const search = f.region('search', ['input']);
  const first = f.region('first', ['one', 'two']); f.region('empty', []); f.region('hidden', ['unavailable'], {enabled: false}); const last = f.region('last', ['three']);
  const groups = tvSettingsResultGroups(['first', 'empty', 'hidden', 'last'], f.registry.entryAddress);
  assert.deepEqual(groups, ['first', 'last']);
  for (const id of groups) f.registry.configure({id, orientation: 'vertical', edges: tvGroupEdges(groups, id, 'search')});
  assert.equal(f.target('first', 'one').nextFocusUp, search);
  assert.equal(f.target('first', 'one').nextFocusDown, undefined);
  assert.equal(f.target('first', 'two').nextFocusDown, last);
  assert.equal(f.target('last', 'three').nextFocusUp, first);
  f.registry.attach({regionId: 'last', childId: 'three'}, null);
  assert.deepEqual(tvSettingsResultGroups(['first', 'empty', 'hidden', 'last'], f.registry.entryAddress), ['first']);
});

test('programme record pairs and later actions retain native horizontal movement and cross vertically to real recovery groups', async () => {
  const f = await focusFixture();
  const order = tvProgrammeGroups({watch: true, record: true, remind: false, later: ['later'], recordError: true, preferenceError: true});
  assert.deepEqual(order, ['watch', 'record', 'favorite', 'later:later', 'record-error', 'preference-error']);
  const ids: Record<string, string[]> = {watch: ['watch'], record: ['record', 'series'], favorite: ['favorite'], 'later:later': ['remind', 'open'], 'record-error': ['retry'], 'preference-error': ['retry']};
  const tags: Record<string, number> = {};
  for (const id of order) tags[id] = f.region(id, ids[id], {orientation: id === 'record' || id.startsWith('later:') ? 'horizontal' : 'vertical', edges: tvGroupEdges(order, id)});
  assert.equal(f.target('record', 'record').nextFocusRight, undefined, 'native geometry can move to Record series');
  assert.equal(f.target('record', 'series').nextFocusLeft, undefined);
  assert.equal(f.target('record', 'record').nextFocusDown, tags.favorite);
  assert.equal(f.target('record', 'series').nextFocusDown, tags.favorite);
  assert.equal(f.target('later:later', 'remind').nextFocusDown, tags['record-error']);
  assert.equal(f.target('later:later', 'open').nextFocusDown, tags['record-error']);
  assert.equal(f.target('record-error', 'retry').nextFocusDown, tags['preference-error']);
  assert.deepEqual(tvProgrammeGroups({watch: false, record: false, remind: true, later: [], recordError: false, preferenceError: false}), ['favorite', 'remind']);
});

test('the TV Server settings pages: the approved owner pages in order, administrators get Dashboard and People only, never Troubleshooting', async () => {
  const pages: any = await import('../src-next/pages/shared/settings/tv-server-pages.ts');
  assert.deepEqual(pages.tvServerPagesFor('owner').map((item: any) => item.id), pages.tvServerPageOrder);
  assert.equal(pages.tvServerPageOrder.includes('troubleshooting'), false);
  assert.deepEqual(pages.tvServerPagesFor('administrator').map((item: any) => item.id), ['dashboard', 'people']);
  assert.equal(pages.isTVServerPage('troubleshooting'), false); assert.equal(pages.isTVServerPage('dashboard'), true);
});

test('screen-reader query rejection emits one sanitized diagnostic and ends cleanly', async () => {
  const host = hooks(), effects: (() => () => void)[] = [], warnings: unknown[][] = [];
  const react = {...host.react, useEffect: (effect: () => () => void) => effects.push(effect)};
  let reject!: (error: Error) => void, removed = 0;
  const module = await screenModule(new URL('../src-next/kit/useScreenReader.ts', import.meta.url), {
    react, 'react-native': {AccessibilityInfo: {addEventListener: () => ({remove() {removed++;}}), isScreenReaderEnabled: () => new Promise((_, fail) => {reject = fail;})}},
  });
  host.render(() => module.useScreenReader()); const cleanup = effects[0]();
  const warn = console.warn;
  try {
    console.warn = (...values: unknown[]) => {warnings.push(values);};
    reject(new Error('untrusted private device error')); await Promise.resolve();
    assert.deepEqual(warnings, [['portico_screen_reader_query_failed']]);
  } finally {console.warn = warn; cleanup();}
  assert.equal(removed, 1);
});
