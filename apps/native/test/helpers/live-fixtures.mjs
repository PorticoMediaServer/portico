// Stand-ins for the Live TV page's drawn header parts in hand-built screen maps (tablet-play6, states 17c).
// The tab pair keeps its real test ids: `live-recordings` is the Recordings tab, `live-tab-guide` the Guide tab,
// each pressable only where the page gave it a handler, as the real Segmented calls only the other tab's. The conflict dot
// keeps its real test id (`live-recordings-conflict`), on the tab and wherever the page draws it.
export function liveTabsFixture(react) {
  const LiveConflictDot = ({style}) => react.createElement('ConflictDot', {testID: 'live-recordings-conflict', style});
  const NativeLiveTabs = ({value, onGuide, onRecordings, conflicts = 0, children}) => react.createElement('LiveTabs', {testID: 'live-header', value, conflicts},
    onGuide || onRecordings ? [
      react.createElement('Tab', {key: 'guide', testID: 'live-tab-guide', selected: value === 'guide', onPress: value === 'guide' ? () => {} : onGuide}),
      react.createElement('Tab', {key: 'recordings', testID: 'live-recordings', selected: value === 'recordings', onPress: value === 'recordings' ? () => {} : onRecordings},
        conflicts > 0 ? react.createElement(LiveConflictDot, null) : null),
    ] : null, children);
  return {'./LiveTabs': {NativeLiveTabs, LiveConflictDot}};
}
export const ownerTagFixture = {'../../kit/shell/ServerIdentity': {OwnerTag: 'OwnerTag'}};

/** Draw the guide page with its real controls, focus grid and programme dialog. */
export async function liveGuideModule(previous, shallow = false) {
  const {screenModule} = await import('./screen-harness.mjs');
  const bindings = livePageBindings(previous);
  const react = bindings.react;
  const element = (type, props, ...children) => react.createElement(type, props, ...children);
  const native = bindings['react-native'];
  const kit = {
    react, 'react-native': native, 'react-native-reanimated': bindings['react-native-reanimated'],
    'react-native-svg': bindings['react-native-svg'], 'react-native-gesture-handler': bindings['react-native-gesture-handler'],
    '../controls': bindings['../../../kit/controls'], '../focus': bindings['../../../kit/focus'],
    '../Icon': bindings['../../../kit/Icon'], '../overlays': bindings['../../../kit/overlays'], '../theme': bindings['../../../kit/theme'],
    '../layout/TVRow': {TVRow: ({children}) => children},
    '../layout/ListEdgeFade': {useWholeItems: () => ({scroll: {}, container: {}, pad: 0, focus() {}, cut: () => false, item: () => ({})})},
    './LivePhonePicker': previous['./LivePhonePicker'],
    './Guide': bindings['../../../kit/live/Guide'],
    './GuideRuler': bindings['../../../kit/live/GuideRuler'],
    './NativeGuideRow': bindings['../../../kit/live/NativeGuideRow'],
    './ProgrammeDetails': bindings['../../../kit/live/ProgrammeDetails'],
    '../../screens/settings-live/tv-settings-focus': previous['./tv-settings-focus'],
  };
  const load = (file, source) => screenModule(new URL(`../../src-next/${file}`, import.meta.url), source);
  if (!shallow) kit['./ProgrammeDetails'] = await load('kit/live/ProgrammeDetails.tsx', {
    ...kit, '../shell/visual': bindings['../../../kit/shell/visual'],
    '../../../../../packages/client-core/src/presentation/kit-model': previous['../../../../../packages/client-core/src/presentation/kit-model'],
  });
  const controls = await load('kit/live/GuideControls.tsx', {...kit, '../browse/Toolbar': {ChipRail: ({children}) => children}});
  const grid = await load('kit/live/NativeGuideGrid.tsx', kit);
  const programme = await load('kit/live/ProgrammePanel.tsx', kit);
  bindings['../../../../../../packages/client-core/src/timer'] = await import('../../../../packages/client-core/src/timer.ts');
  const hook = await load('pages/shared/live/useLiveGuide.ts', bindings);
  const frame = ({actions, tabs, chips, toolbar, list, overlay}) => element('GuidePage', shallow ? {list, toolbar, overlay} : null, actions, tabs, chips, toolbar, list, overlay);
  const content = await load('kit/live/GuideContent.tsx', {
    ...liveContentBindings(bindings), './GuideControls': controls, './NativeGuideGrid': grid, './ProgrammePanel': programme,
    './LivePhonePicker': previous['./LivePhonePicker'],
  });
  const inputs = {
    ...bindings, '../../shared/live/useLiveGuide': hook, '../../../kit/live/GuideContent': content,
    './RecordingsPage': {RecordingsPage: previous['./RecordingsScreen']?.NativeRecordingsScreen},
    // The owner editor the tests stub as './LiveOwnerScreen' is the touch LiveOwnerPage now.
    './LiveOwnerPage': {LiveOwnerPage: previous['./LiveOwnerScreen']?.NativeLiveOwnerScreen ?? 'LiveOwnerPage'},
    '../../../kit/frames': {LiveFrame: frame},
  };
  const [touch, tv] = await Promise.all(['touch', 'tv'].map(form => load(`pages/${form}/live/GuidePage.tsx`, inputs)));
  return {NativeLiveScreen: props => {
    const page = bindings['../../../kit/theme'].useTheme().isTV ? tv : touch;
    const result = page.GuidePage(props);
    return shallow ? frame(result.props) : result;
  }};
}

function livePageBindings(previous) {
  return Object.fromEntries(Object.entries(previous).map(([path, value]) => {
    if (path.startsWith('../../../../../packages/')) return ['../' + path, value];
    if (path.startsWith('../../')) return ['../' + path, value];
    if (path === './LiveTabs') return ['../../../kit/live/LiveTabs', value];
    if (path.startsWith('../home/')) return [path.replace('../home/', '../../../screens/home/'), value];
    if (path.startsWith('../destinations/')) return [path.replace('../destinations/', '../../../screens/destinations/'), value];
    if (path.startsWith('./')) return [path.replace('./', '../../../screens/settings-live/'), value];
    return [path, value];
  }));
}


function liveContentBindings(bindings) {
  return Object.fromEntries(Object.entries(bindings).map(([path, value]) => [path
    .replace(/^\.\.\/\.\.\/\.\.\/kit\/live\//, './')
    .replace(/^\.\.\/\.\.\/\.\.\/kit\//, '../')
    .replace(/^\.\.\/\.\.\/\.\.\/(screens|tv)\//, '../../$1/')
    .replace(/^\.\.\/\.\.\/\.\.\/\.\.\/\.\.\/\.\.\/packages\//, '../../../../../packages/'), value]));
}

export async function recordingsPageModule(previous) {
  const {screenModule} = await import('./screen-harness.mjs');
  const bindings = livePageBindings(previous), react = bindings.react;
  const load = (file, modules) => screenModule(new URL(`../../src-next/${file}`, import.meta.url), modules);
  const render = (type, props, ...children) => react.createElement(type, props, ...children);
  const kit = {
    react, 'react-native': bindings['react-native'],
    '../controls': previous['../../kit/controls'], '../controls/Button': {Button: previous['../../kit/controls'].Button},
    '../controls/Focusable': {Focusable: previous['../../kit/controls'].Focusable},
    '../dock/DockClearance': {useDockClearance: () => 0},
    '../focus': previous['../../kit/focus'], '../theme': previous['../../kit/theme'],
    '../layout/TVRow': {TVRow: ({children}) => children}, '../layout/ListEdgeFade': {ListEdgeFade: 'ListEdgeFade'},
    '../overlays': previous['../../kit/overlays'], '../states': previous['../../kit/states'], '../Icon': previous['../../kit/Icon'],
    '../artwork': {Artwork: 'Artwork', ChannelMark: 'ChannelMark', GeneratedArt: 'GeneratedArt'},
    '../cards/primitives': {Progress: 'Progress'},
    '../frames/metrics': {useFrameMetrics: () => ({form: previous['../../kit/theme'].useTheme().isTV ? 'tv' : 'phone',
      gutter: 20, rightGutter: 20, cardInset: 0, cardRadius: 12, cardPadding: 16})},
    '../../../../../packages/i18n/src': previous['../../../../../packages/i18n/src'],
    '../../../../../packages/client-core/src/presentation/title-page': previous['../../../../../packages/client-core/src/presentation/title-page'],
    '../../../../../packages/client-core/src/dvr': previous['../../../../../packages/client-core/src/dvr'],
    '../../screens/settings-live/recording-rule-presentation': previous['./recording-rule-presentation'],
    './Guide': {Flag: 'Flag', guideSpec: {timeColumn: {phone: 60, tv: 190}, mark: {phone: 32}}},
  };
  kit['../controls'] = {...kit['../controls'], Tabs: ({items, onValueChange, itemProps}) =>
    render('Tabs', {}, items.map(item => render('Chip', {key: item.value, ...itemProps(item), onPress: () => onValueChange(item.value)}, item.label)))};
  const parts = await load('kit/live/Recordings.tsx', kit);
  const state = await load('pages/shared/live/useRecordings.ts', bindings);
  const dialogs = await load('pages/shared/live/useRecordingDialogs.ts', bindings);
  const sheets = await load('kit/live/RecordingDialogs.tsx', {
    ...kit, '../../pages/shared/live/useRecordingDialogs': dialogs, '../../pages/shared/live/useRecordings': state,
  });
  const content = await load('kit/live/RecordingsContent.tsx', {
    ...liveContentBindings(bindings), '../../pages/shared/live/useRecordings': state,
    './RecordingDialogs': sheets, './Recordings': parts,
  });
  const form = previous['../../kit/theme'].useTheme().isTV ? 'tv' : 'touch';
  const page = await load(`pages/${form}/live/RecordingsPage.tsx`, {
    ...bindings, '../../shared/live/useRecordings': state, '../../../kit/live/RecordingsContent': content,
    '../../../kit/frames': {LiveFrame: ({back, actions, tabs, facts, footer, list, overlay}) =>
      render('RecordingsPage', {}, back && render('Button', back), actions, tabs, facts, list, footer, overlay)},
  });
  return {NativeRecordingsScreen: page.RecordingsPage};
}
