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
    './useHostRef': {useHostRef: () => ({native: {current: null}, ref() {}})},
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

test('server menu exposes the approved owner pages, restricts administrators and changes its pane on focus', async () => {
  const {react} = hooks(), selected: string[] = [];
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TVServerPageMenu.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec},
    '../../../../../../packages/design/src/next-icons': {nextIcons: Object.fromEntries(settingsSpec.map(section => [`i-${section.icon}`, {}]))},
    '../../../kit/controls': {Focusable: 'Focusable'}, '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/shell': {ServerHeader: 'ServerHeader'}, '../../../kit/shell/visual': {shellType: (fontSize: number) => ({fontSize})}, '../../../kit/theme': {useTheme: () => theme},
  });
  const props = {role: 'owner', page: 'general', serverName: 'Server', runtime: {text: {t: (id: string) => id}}, column: 'pages', rows: 'rows', rail: 'rail', onSelect: (id: string) => selected.push(id)};
  const tree = module.TVServerPageMenu(props), rows = nodes(tree).filter(node => node.type === 'Focusable');
  assert.deepEqual(rows.map(node => node.props.focusId), module.tvServerPageOrder);
  assert.equal(rows.some(node => node.props.focusId === 'troubleshooting'), false);
  assert.equal(tree.props.style.width, 480);
  rows[0].props.onFocus(); assert.deepEqual(selected, []);
  rows[1].props.onFocus(); assert.deepEqual(selected, ['dashboard']);
  assert.equal(nodes(rows[0]).find(node => node.type === 'View')!.props.style.width, 4);
  assert.equal(nodes(rows[1]).find(node => node.type === 'Icon')!.props.color, 'tertiary');
  assert.equal(nodes(rows[1]).find(node => node.type === 'Text')!.props.style[1].color, 'secondary');
  const admin = module.TVServerPageMenu({...props, role: 'administrator', page: 'dashboard'});
  assert.deepEqual(nodes(admin).filter(node => node.type === 'Focusable').map(node => node.props.focusId), ['dashboard', 'people']);
  assert.equal(module.isTVServerPage('troubleshooting'), false);
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
