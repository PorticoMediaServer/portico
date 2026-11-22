import {screenModule} from './screen-harness.mjs';

const load = (path, modules) => screenModule(new URL(`../../src-next/${path}`, import.meta.url), modules);

export async function settingsFixture(previous) {
  const modules = {};
  for (const [path, value] of Object.entries(previous)) {
    const revised = path.startsWith('../../../../../packages/') ? '../' + path
      : path.startsWith('../../') ? '../' + path
        : path.startsWith('../') ? path.replace('../', '../../../screens/')
          : path.startsWith('./') ? path.replace('./', '../../../screens/settings-live/') : path;
    modules[revised] = value;
  }
  modules['react-native'] = {Keyboard: {dismiss() {}}, ...modules['react-native']};
  // A stub for a module that now lives beside these pages (pages/shared/settings) also answers their sibling import.
  for (const [path, value] of Object.entries(modules)) {
    const sibling = /^\.\.\/\.\.\/\.\.\/pages\/shared\/settings\/([^/]+)$/.exec(path)?.[1];
    if (sibling) modules[`./${sibling}`] ??= value;
    // …and a stub for a touch or TV page answers the shared pages' `../../touch/…` and `../../tv/…` imports of it.
    const formPage = /^\.\.\/\.\.\/\.\.\/pages\/((?:touch|tv)\/.+)$/.exec(path)?.[1];
    if (formPage) modules[`../../${formPage}`] ??= value;
  }
  // This device's playback rows now come from pages/shared/settings/DevicePlaybackRows (components) and useDeviceRows
  // (focus ids, Safe mode availability); the tests' per-row stubs are merged into those two modules.
  const rowStubs = Object.entries(modules).filter(([path]) => /screens\/settings-live\/Native(SafeMode|ApplePassthrough|FrameRate|Passthrough|DynamicRange|LossNotices)Row$/.test(path)).map(([, value]) => value);
  if (rowStubs.length) {
    const merged = Object.assign({}, ...rowStubs);
    modules['./DevicePlaybackRows'] ??= merged;
    modules['./useDeviceRows'] ??= merged;
  }
  // A TV's More links (Watch Together) are offered when their routes are registered, as they are in the app.
  modules['../../../shell/account'] ??= {nativeMenuRouteAvailable: () => true};
  const react = modules.react;
  const e = react.createElement ?? react.default.createElement;
  const FocusRegion = modules['../../../kit/focus'].FocusRegion;
  const fixtureFrame = {SettingsListFrame: ({bar, search, list, profile, shortcuts, page, hints}) => e('View', null,
    e('Text', null, bar.title), e('Button', {testID: bar.action.testID, onPress: bar.action.onPress}), search, profile, shortcuts, list, page, hints),
  SettingsPageFrame: ({title, back, panel, children, scrollRef, onScrollLayout}) => e('View', null,
    back && e('PhoneHeader', {title, onBack: back.onPress}), panel ?? e('ScrollView', {ref: scrollRef, onLayout: event => onScrollLayout?.(event.nativeEvent.layout.height)}, children)),
  SectionCard: 'SectionCard'};
  const parts = {SettingsPageLink: ({id, ...props}) => e('Focusable', {...props, testID: `settings-page-${id}`}),
    SettingsNavigationGroup: 'Group', SettingsControlRows: FocusRegion, SettingsManagementCard: 'ManagementCard',
    SettingsHelp: 'Text', SettingsNotificationCount: 'Text', SettingsSearchGroup: 'Group',
    SettingsKeyHints: () => e('View', {testID: 'settings-key-hints'}, 'Settings remote hints'),
    PhoneDashboardCard: props => e('Focusable', {...props, testID: 'settings-dashboard-card'})};
  const model = await load('pages/shared/settings/useSettingsPage.ts', modules);
  // A narrow fixture can mount just the Settings destinations. The full body uses
  // the same real row data and command hook when these tests open it.
  const prefs = await load('pages/shared/settings/useSettingsPreferences.ts', modules);
  const bindings = {...modules, '@portico/design': {frameSpec: {settings: {tvSearchWidth: 500, languageWidth: 640}}},
    '../../../kit/settings/SettingsParts': parts, '../../../kit/settings/AdvancedSection': {NativeAdvancedSection: 'NativeAdvancedSection'}, '../../../kit/frames': fixtureFrame, '../../../kit/overlays/surface': {OverlayFootRow: 'Footer'},
    '../../../kit/shell/SettingsProfileCard': {SettingsProfileCard: 'Focusable'}, './useSettingsPreferences': prefs,
    // The subtitle preview's palette is plain data; the real one is used.
    '../../../platform/playback/caption-style': modules['../../../platform/playback/caption-style'] ?? await import('../../src-next/platform/playback/caption-style.ts')};
  const body = await load('pages/shared/settings/SettingsPreferences.tsx', bindings);
  const Metadata = previous['../../touch/settings/ServerMetadataPage']?.ServerMetadataPage;
  const People = previous['../../touch/settings/ServerPeoplePage']?.ServerPeoplePage;
  const History = previous['../../touch/settings/ServerHistoryPage']?.ServerHistoryPage;
  const SignIn = previous['../../touch/settings/ServerSignInPage']?.ServerSignInPage ?? 'ServerSignInPage';
  const page = await load('pages/shared/settings/SettingsPage.tsx', {...bindings, './useSettingsPage': model, './SettingsPreferences': body,
    '../../../kit/search/TVKeyboard': previous['../../../kit/search/TVKeyboard'] ?? {NativeTVKeyboard: 'NativeTVKeyboard'},
    '../../../kit/settings/remote-sources/NativeRemoteSourcesScreen': previous['../../../kit/settings/remote-sources/NativeRemoteSourcesScreen'] ?? {NativeRemoteSourcesScreen: 'NativeRemoteSourcesScreen'},
    './ServerGeneralPage': previous['./ServerGeneralPage'] ?? {ServerGeneralPage: 'ServerGeneralPage'},
    '../../touch/live/LiveOwnerPage': previous['../../touch/live/LiveOwnerPage'] ?? {LiveOwnerPage: 'LiveOwnerPage'},
    './ServerRemoteAccessPage': previous['./ServerRemoteAccessPage'] ?? {ServerRemoteAccessPage: 'ServerRemoteAccessPage'},
    '../../touch/settings/ServerTroubleshootingPage': previous['../../touch/settings/ServerTroubleshootingPage'] ?? {ServerTroubleshootingPage: 'ServerTroubleshootingPage'},
    '../../touch/settings/ServerStreamingPage': previous['../../touch/settings/ServerStreamingPage'] ?? {ServerStreamingPage: 'ServerStreamingPage'},
    '../../touch/settings/ServerSegmentsPage': previous['../../touch/settings/ServerSegmentsPage'] ?? {ServerSegmentsPage: 'ServerSegmentsPage'},
    '../../touch/settings/ServerIntegrationsPage': previous['../../touch/settings/ServerIntegrationsPage'] ?? {ServerIntegrationsPage: 'ServerIntegrationsPage'},
    '../../touch/settings/ServerStoragePage': previous['../../touch/settings/ServerStoragePage'] ?? {ServerStoragePage: 'ServerStoragePage'},
    '../../touch/settings/ServerLibrariesPage': previous['../../touch/settings/ServerLibrariesPage'] ?? {ServerLibrariesPage: 'ServerLibrariesPage'},
    '../../touch/settings/ServerMetadataPage': {ServerMetadataPage: function MetadataPage({back}) {return e(fixtureFrame.SettingsPageFrame, {title: 'metadata', back}, e(Metadata));}},
    '../../touch/settings/ServerPeoplePage': {ServerPeoplePage: function PeoplePage({back}) {return e(fixtureFrame.SettingsPageFrame, {title: 'people', back}, e(People));}},
    '../../touch/settings/ServerHistoryPage': {ServerHistoryPage: function HistoryPage({back}) {return e(fixtureFrame.SettingsPageFrame, {title: 'history', back}, e(History));}},
    '../../touch/settings/ServerSignInPage': {ServerSignInPage: function SignInPage({back}) {return e(fixtureFrame.SettingsPageFrame, {title: 'signin', back}, e(SignIn));}}});
  return {NativeSettingsScreen: page.NativeSettingsPage};
}

export function settingsFixtureChildren(element) {
  return ['SettingsListFrame', 'SettingsPageFrame', 'SettingsPageLink', 'SettingsKeyHints', 'PhoneDashboardCard', 'HistoryPage', 'PeoplePage', 'MetadataPage', 'SignInPage'].includes(element?.type?.name)
    ? element.type(element.props) : element?.props?.children;
}
