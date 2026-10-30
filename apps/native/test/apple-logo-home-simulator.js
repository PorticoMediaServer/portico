/* Account-free screenshot entry. Renders the actual NativeShell/HomeScreen and chrome;
 * only the empty public Home document and its nonpersistent runtime are fixtures. */
require('../src-next/platform/device-kind');
require('../src-next/platform/legacy-events');
require('../src-next/platform/random-id');
require('@bacons/text-decoder/install');
require('react-native-url-polyfill/auto');
const React = require('react');
const {AppRegistry, LogBox, NativeModules, Platform, StatusBar, Text, View} = require('react-native');
const {GestureHandlerRootView} = require('react-native-gesture-handler');
const {SafeAreaProvider} = require('react-native-safe-area-context');
const {ThemeProvider, useTheme} = require('../src-next/kit/theme');
const {FocusProvider, createFocusRegistry, requestNativeFocus} = require('../src-next/kit/focus');
const {NativeShell} = require('../src-next/shell/NativeShell');
const {tvFocusIds} = require('../src-next/shell/tv-focus');
const {reportFirstFrame} = require('../src-next/platform/boot-host');
const {defaultI18n} = require('../../../packages/i18n/src');
// Keep the account-free review image free of the Debug-only warning toast; console logs are retained.
LogBox.ignoreAllLogs();
const noop = () => {}, subscribe = () => noop, resolved = async () => {};
const authority = {serverId: 'logo-public', accountId: 'logo-public', profileId: 'logo-public', epoch: '1', viewerFence: 'logo-public'};
const snapshot = {authority, destination: {kind: 'home'}, selectionActive: false, homeObscured: false, homeVisible: true, mediaExpanded: false,
  libraries: [], hasLiveTV: false, profile: {id: 'logo-public', name: 'Guest', initial: 'G'}, unreadNotifications: false,
  account: {accountLine: 'Public simulator fixture', actions: [], profiles: [], viewerRole: 'owner', serverAction: {id: 'activity', label: 'Server activity', onPress: noop}}};
const home = {phase: 'ready', paging: new Set(), document: {serverId: authority.serverId, viewerFence: authority.viewerFence, rows: [], spotlight: [],
  layout: {rowOrder: [], hiddenRowIds: []}, revision: 1, generatedAt: '2026-10-28T12:00:00Z'}};
const registry = createFocusRegistry();
function EmptyHome() {const t = useTheme(); return React.createElement(View, {style: {flex: 1, alignItems: 'center', justifyContent: 'center'}},
  React.createElement(Text, {style: [t.type.heading, {color: t.colors.text}]}, 'Home'),
  React.createElement(Text, {style: [t.type.body, {color: t.colors.textSecondary, marginTop: 12}]}, 'Your library starts here.'));}
const runtime = {subscribe, getSnapshot: () => snapshot, home: {subscribe, getSnapshot: () => home, load: resolved, revalidate: resolved, retry: resolved, setRowDemand: noop},
  memory: {read: async () => undefined, readSidebar: async () => true, rememberSidebar: resolved},
  text: {t: defaultI18n.t, locale: 'en', timeZone: 'UTC'}, artwork: noop, ambient: noop,
  actions: {navigate: noop, play: noop, more: noop, myList: resolved, customize: noop}, selection: () => ({}), setHomeWindow: noop,
  renderSelection: noop, renderRoutes: noop, renderDestination: noop, renderHomeEmpty: () => React.createElement(EmptyHome),
  error: () => ({}), reportError: error => NativeModules.PorticoRuntime.recordTestEvent(JSON.stringify({logoError: String(error)})),
  onDestination: noop, scrollToTop: noop, registerScrollToTop: () => noop, onArtworkError: noop};
function Entry() {
  React.useEffect(() => {if (!Platform.isTV) return; let attempts = 0; const timer = setInterval(() => {
    const host = registry.child({regionId: tvFocusIds(authority).rail, childId: 'home'});
    if (host && requestNativeFocus(host) || ++attempts > 40) clearInterval(timer);
  }, 100); return () => clearInterval(timer);}, []);
  return React.createElement(GestureHandlerRootView, {style: {flex: 1}, onLayout: reportFirstFrame},
    React.createElement(SafeAreaProvider, null, React.createElement(ThemeProvider, {reduceMotion: true},
      React.createElement(FocusProvider, {registry}, React.createElement(View, {style: {flex: 1, backgroundColor: '#08090b'}},
        React.createElement(StatusBar, {barStyle: 'light-content'}), React.createElement(NativeShell, {runtime}))))));
}
AppRegistry.registerComponent('Portico', () => Entry);
