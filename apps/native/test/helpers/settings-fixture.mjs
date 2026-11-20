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
    '../../../kit/shell/SettingsProfileCard': {SettingsProfileCard: 'Focusable'}, './useSettingsPreferences': prefs};
  const body = await load('pages/shared/settings/SettingsPreferences.tsx', bindings);
  const History = previous['../../touch/settings/ServerHistoryPage']?.ServerHistoryPage;
  const page = await load('pages/shared/settings/SettingsPage.tsx', {...bindings, './useSettingsPage': model, './SettingsPreferences': body,
    './ServerGeneralPage': previous['./ServerGeneralPage'] ?? {ServerGeneralPage: 'ServerGeneralPage'},
    './ServerRemoteAccessPage': previous['./ServerRemoteAccessPage'] ?? {ServerRemoteAccessPage: 'ServerRemoteAccessPage'},
    '../../touch/settings/ServerStreamingPage': previous['../../touch/settings/ServerStreamingPage'] ?? {ServerStreamingPage: 'ServerStreamingPage'},
    '../../touch/settings/ServerSegmentsPage': previous['../../touch/settings/ServerSegmentsPage'] ?? {ServerSegmentsPage: 'ServerSegmentsPage'},
    '../../touch/settings/ServerIntegrationsPage': previous['../../touch/settings/ServerIntegrationsPage'] ?? {ServerIntegrationsPage: 'ServerIntegrationsPage'},
    '../../touch/settings/ServerLiveOwnerPage': previous['../../touch/settings/ServerLiveOwnerPage'] ?? {ServerLiveOwnerPage: 'ServerLiveOwnerPage'},
    '../../touch/settings/ServerHistoryPage': {ServerHistoryPage: function HistoryPage({back}) {return e(fixtureFrame.SettingsPageFrame, {title: 'history', back}, e(History));}}});
  return {NativeSettingsScreen: page.NativeSettingsPage};
}

export function settingsFixtureChildren(element) {
  return ['SettingsListFrame', 'SettingsPageFrame', 'SettingsPageLink', 'SettingsKeyHints', 'PhoneDashboardCard', 'HistoryPage'].includes(element?.type?.name)
    ? element.type(element.props) : element?.props?.children;
}
