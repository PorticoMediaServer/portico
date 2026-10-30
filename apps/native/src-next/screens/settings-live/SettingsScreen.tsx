import {useTVVoiceSearch} from './use-tv-voice-search';
import {tvGroupEdges, tvSettingsResultGroups} from './tv-settings-focus';
import {passthroughFormats} from '../../platform/playback/passthrough-formats';
import {NativeDynamicRangeRow, nativeDynamicRangeFocusId} from './NativeDynamicRangeRow';
import {nativeDynamicRangeMatchingAvailable} from '../../platform/playback/dynamic-range-matching';
import {NativeApplePassthroughRow} from './NativeApplePassthroughRow';
import {NativeCapabilityRows} from './NativeCapabilityRows';
import {NativeFrameRateRow, nativeFrameRateFocusId} from './NativeFrameRateRow';
import {NativePassthroughRow, nativePassthroughFocusId} from './NativePassthroughRow';
import {NativeLossNoticesRow, nativeLossNoticesFocusId} from './NativeLossNoticesRow';
import {NativeSafeModeRow, nativeSafeModeAvailable, nativeSafeModeFocusId} from './NativeSafeModeRow';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, Dimensions, NativeModules, Platform, ScrollView, StatusBar, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {settingsStructure, settingsScopeLabel, settingsRowId, searchSettings, readComposite, compositePatch, type SettingsPage, type SettingsRow as Row} from '../../../../../packages/client-core/src/presentation/settings-structure';
import type {PreferenceScope} from '../../../../../packages/client-core/src/preferences';
import {COMMON_LANGUAGES, languageName, MAX_LANGUAGES} from '../../../../../packages/client-core/src/presentation/language-list';
import {localeChoices} from '../../../../../packages/client-core/src/presentation/region-choices';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {resetRecommendations} from '../../../../../packages/client-core/src/recommendation-feedback';
import {nextIcons} from '../../../../../packages/design/src/next-icons';
import {Button, Focusable, Select, Switch, TextField} from '../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {Icon, type IconName} from '../../kit/Icon';
import {ChoiceMatrix, SettingsRow, SettingsSheet, SettingsSearchResults, LanguageList, ShortcutTiles, type LanguageOption} from '../../kit/shell';
import {ProfileFace} from '../../kit/artwork';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {StateView} from '../../kit/states';
import {stateSentenceParts} from '../../kit/states/sentence-parts';
import {Dialog, Menu} from '../../kit/overlays';
import type {NativeShellRuntime} from '../../shell/runtime';
import {tvFocusIds} from '../../shell/tv-focus';
import {NativeTVKeyboard} from '../destinations';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import type {NativeSettingsLivePorts} from './ports';
import type {NativeSettingsController} from './settings-controller';
import type {NativeDevicePreferences} from './device-preference-consumer';
import {setNativeScreenChromeHidden} from '../../shell/chrome';
import {Screen, useScreenBack, useScreenEntry} from './Screen';
import {nativeSettingsActionTarget} from './settings-actions';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {parseSettingsConfiguration} from '../../../../../packages/client-core/src/settings-configuration';
import {readSettingsBinding, settingsBinding, type SettingsBindingValue} from '../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec} from '../../../../../packages/client-core/src/presentation/settings-spec';
import {NativeOwnerLibrariesPanel} from './owner-libraries/OwnerLibrariesPanel';
import {ServerBand, ServerHeader} from '../../kit/shell/ServerIdentity';
import {alpha} from '../../kit/shell/visual';
import {NativeLiveOwnerScreen} from './LiveOwnerScreen';
import {NativeLiveOwnerController} from './live-owner-controller';
import {createNativeRecordingsController} from './recordings-controller';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {NativeServerTroubleshooting} from './server/TroubleshootingPage';
import {NativeServerStorage} from './server/StoragePage';
import {NativeServerIntegrations} from './server/IntegrationsPage';
import {NativeServerGeneral} from './server/GeneralPage';
import {NativeRemoteAccessPage} from './server/RemoteAccessPage';
import {NativeServerStreaming} from './server/StreamingPage';
import {NativeServerSegments} from './server/SegmentsPage';
import {NativeServerPeople} from './server/PeoplePage';
import {FormScrollView} from '../../platform/keyboard';
import {NativeServerSignIn} from './server/SignInPage';
import {NativeServerMetadata} from './server/MetadataPage';
import {NativeServerHistory} from './server/HistoryPage';
import {PhoneServerPageHeader} from './server/PhoneServerPageHeader';
import {NativeRemoteSourcesScreen} from '../settings/remote-sources/NativeRemoteSourcesScreen';
import {NativeDashboardScreen} from './DashboardScreen';
import type {NativeDashboardController} from './dashboard-controller';
import QRCode from 'react-native-qrcode-svg';
import {nativeEqualizer} from '../../platform/playback/equalizer';
import {publicTVHandoffAddress} from '../../tv/auth/TVHandoff';

const SKIP_KEYS: readonly string[] = ['playback.introSkip', 'playback.creditsSkip', 'playback.recapSkip'];

const noFocusSubscription = () => () => {};
const noFocusSnapshot = () => 0;

/** settings.html wins over the structure's group order. */
const serverPageOrder = ['general', 'dashboard', 'history', 'libraries', 'remote-sources', 'metadata', 'segments', 'live', 'people', 'signin', 'remote', 'integrations', 'streaming', 'storage', 'troubleshooting'];

/** Same icon normalization as settings.html: strip one leading i-, look up, fall back. */
export function nativeSettingsIcon(name: string): IconName {
  const key = `i-${name.replace(/^i-/, '')}`;
  return Object.hasOwn(nextIcons, key) ? key as IconName : 'i-sliders';
}

export function NativeSettingsScreen({ports, controller, runtime, devicePreferences, dashboard, initialPage, initialChannelId}: {ports: NativeSettingsLivePorts; controller: NativeSettingsController; runtime: NativeShellRuntime; devicePreferences: NativeDevicePreferences; dashboard?: NativeDashboardController; initialPage?: 'live'; initialChannelId?: string}) {
  const theme = useTheme(), t = runtime.text.t, registry = useFocusRegistry(), insets = useSafeAreaInsets();
  const shell = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const navigation = ports.context.navigation, route = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const opening = useMemo(() => ({retired: false, controller, runtime, context: ports.context, route: route.route, revision: route.revision}), [controller, runtime, ports.context, route.route, route.revision]);
  const openingRef = useRef(opening);
  if (openingRef.current !== opening) openingRef.current.retired = true;
  openingRef.current = opening;
  useLayoutEffect(() => {opening.retired = false; return () => {opening.retired = true;};}, [opening]);
  const routeCurrent = useCallback(() => {
    const owns = () => !opening.retired && openingRef.current === opening && !runtime.retirementSignal?.aborted;
    if (!owns() || !controller.lifetime.current() || !owns() || !ports.context.current() || !owns() || !navigation.isCurrent() || !owns()) return false;
    const now = navigation.getSnapshot();
    // The trusted renderer may also draw Settings as a denied server-page fallback.
    // Match its actual original route; do not assume every such opening is a shell destination.
    return owns() && now.route === route.route && now.revision === route.revision && controller.lifetime.current() && owns();
  }, [controller, runtime, ports.context, navigation, route.route, route.revision, opening]);
  const structure = settingsStructure(ports.settingsContext());
  const accountPages = useMemo(() => structure.headings.find(heading => heading.id === 'account')?.pages ?? [], [structure]);
  const managesServer = ports.context.account.account.role === 'owner';
  const compact = !theme.isTV && theme.width < 600;
  // quality.awayLimit is the owner's own server limit, read from the same
  // settings-configuration document the Streaming page reads. The route decides
  // who may see it; a refusal or an unreadable answer leaves the rows out.
  // What the television itself reports, read live: HDR from the display's own modes
  // and surround from the audio output's channels. It only fills the drawn choice
  // until the viewer overrides it; it is never written to the device record.
  const [reportedOutputs, setReportedOutputs] = useState<Readonly<{hdr: boolean; surround: boolean}>>();
  useEffect(() => {
    let alive = true;
    const read = async () => {
      try {
        const facts = await (NativeModules.PorticoCapabilities?.facts?.() as Promise<Record<string, unknown>> | undefined);
        if (!alive || !facts) return;
        const modes = facts.hdrModes && typeof facts.hdrModes === 'object' ? facts.hdrModes as Record<string, unknown> : undefined;
        const display = facts.display && typeof facts.display === 'object' ? facts.display as Record<string, unknown> : undefined;
        const audio = facts.audio && typeof facts.audio === 'object' ? facts.audio as Record<string, unknown> : undefined;
        const hdr = modes ? [modes.hdr10, modes.hlg, modes.dolbyVision].some(value => value === true)
          : typeof facts.hdr === 'boolean' ? facts.hdr : typeof display?.hdr === 'boolean' ? display.hdr : undefined;
        const channels = typeof audio?.maxOutputChannels === 'number' ? audio.maxOutputChannels
          : typeof audio?.outputNumberOfChannels === 'number' ? audio.outputNumberOfChannels : undefined;
        if (hdr === undefined || channels === undefined) return;
        setReportedOutputs({hdr, surround: channels > 2});
      } catch {}
    };
    void read();
    const subscription = AppState.addEventListener('change', value => {if (value === 'active') void read();});
    return () => {alive = false; subscription.remove();};
  }, []);
  const [awayLimit, setAwayLimit] = useState<SettingsBindingValue>();
  useEffect(() => {
    if (!managesServer) return;
    let alive = true;
    void controller.lifetime.request(next => ports.context.api.request<unknown>(routePath('get_settings_configuration'), 'GET', undefined, next))
      .then(parseSettingsConfiguration).then(configuration => {
        const binding = settingsBinding('streaming.awayQuality');
        const value = binding ? readSettingsBinding(binding, {configuration}) : undefined;
        if (alive && controller.lifetime.current()) setAwayLimit(value);
      }).catch(() => {});
    return () => {alive = false;};
  }, [controller, managesServer, ports]);
  // The status uses the Away from home row's own labels for the ladder rung;
  // a value no option names stays hidden rather than printing a raw code.
  const awayLimitLabel = useMemo(() => {
    const code = awayLimit && typeof awayLimit.effective === 'string' ? awayLimit.effective : undefined;
    if (!code) return undefined;
    const page = settingsSpec.find(value => value.id === 'quality' && value.scope === 'account');
    const row = page?.groups?.flatMap(group => group.rows).find(value => value.id === 'quality.remote');
    const control = row && 'control' in row ? row.control : undefined;
    const choice = control && (control.type === 'select' || control.type === 'segmented') ? control.options?.find(value => String(value.value) === code) : undefined;
    return choice ? t(choice.label) : undefined;
  }, [awayLimit, t]);
  // settings.html wins over the structure's group order. Every listed page has
  // a native renderer. The shared structure predates its Sign-in entry.
  const serverPages = useMemo(() => {
    if (theme.isTV) return [];
    const pages = (structure.headings.find(heading => heading.id === 'server')?.pages ?? []).filter(value => value.id === 'dashboard' || value.id === 'libraries' || value.id === 'storage' || value.id === 'troubleshooting' || value.id === 'people' && ports.context.account.capabilities.managePeople || managesServer && (value.id === 'general' || value.id === 'history' || value.id === 'metadata' || value.id === 'people' || value.id === 'remote' || value.id === 'live' || value.id === 'integrations' || value.id === 'segments' || value.id === 'remote-sources' || value.id === 'streaming'));
    const signIn = settingsSpec.find(value => value.scope === 'server' && value.id === 'signin');
    if (managesServer && ports.context.account.capabilities.manageSignIn && signIn) pages.push({id: 'signin', title: signIn.title, icon: signIn.icon as SettingsPage['icon'], sections: []});
    return [...pages].sort((a, b) => {
      const left = serverPageOrder.indexOf(a.id), right = serverPageOrder.indexOf(b.id);
      return (left < 0 ? serverPageOrder.length : left) - (right < 0 ? serverPageOrder.length : right);
    });
  }, [structure, theme.isTV, managesServer, ports]);
  // phone-app 8 omits the profile row because the drawn profile card replaces it.
  const phoneAccountOrder = ['playback', 'reading', 'languages', 'quality', 'music', 'appearance', 'region', 'privacy', 'device'];
  const tabletAccountOrder = ['profile', 'privacy', 'playback', 'reading', 'languages', 'quality', 'music', 'appearance', 'region', 'device'];
  const tabletAccountClusters: readonly (readonly string[])[] = [['profile', 'privacy'], ['playback', 'reading', 'languages', 'quality', 'music'], ['appearance', 'region', 'device']];
  const tabletServerClusters: readonly (readonly string[])[] = [['general', 'dashboard', 'history'], ['libraries', 'remote-sources', 'metadata', 'segments'], ['live', 'people', 'signin', 'remote', 'integrations'], ['streaming', 'storage', 'troubleshooting']];
  const tabletDefaultPage = accountPages.some(value => value.id === 'playback') ? 'playback' : accountPages[0]?.id;
  const [selected, setSelected] = useState(() => !theme.isTV && managesServer && initialPage ? initialPage : !theme.isTV && !compact ? tabletDefaultPage : accountPages[0]?.id), [query, setQuery] = useState(''), [pushed, setPushed] = useState(!theme.isTV && (!!initialPage || !compact));
  // Once the wide pane is visible, shrinking keeps that same editor on screen.
  // Explicit phone Back still returns to the hub and releases its page normally.
  useLayoutEffect(() => {if (!theme.isTV && !compact) setPushed(true);}, [compact, theme.isTV]);
  const pageState = useRef({selected, pushed, compact, television: theme.isTV});
  pageState.current = {selected, pushed, compact, television: theme.isTV};
  const viewCurrent = useCallback(() => routeCurrent() && pageState.current.selected === selected && pageState.current.pushed === pushed
    && pageState.current.compact === compact && pageState.current.television === theme.isTV, [routeCurrent, selected, pushed, compact, theme.isTV]);
  const popPage = () => {if (!viewCurrent()) return; pageState.current = {...pageState.current, pushed: false}; setPushed(false);};
  // tv-more frame 22: the list keeps the spec's three groups, in the drawn order; Reading is not on TV.
  const tvPageOrder = ['profile', 'privacy', 'playback', 'languages', 'quality', 'music', 'appearance', 'region', 'device'];
  const tvPageGroups: readonly (readonly string[])[] = [['profile', 'privacy'], ['playback', 'languages', 'quality', 'music'], ['appearance', 'region', 'device']];
  const accountOrder = theme.isTV ? tvPageOrder : compact ? phoneAccountOrder : tabletAccountOrder;
  const orderedPages = [
    ...accountOrder.flatMap(id => accountPages.filter(value => value.id === id)),
    ...accountPages.filter(value => !accountOrder.includes(value.id) && !(compact && value.id === 'profile')),
  ];
  const grouped = (pages: readonly SettingsPage[], clusters: readonly (readonly string[])[]) => {
    const seen = new Set(clusters.flat());
    const groups = clusters.map(ids => ids.flatMap(id => pages.filter(value => value.id === id))).filter(group => group.length);
    const rest = pages.filter(value => !seen.has(value.id));
    if (rest.length) groups.push(rest);
    return groups;
  };
  const pageRow = (value: SettingsPage) => <Focusable key={value.id} focusId={value.id} testID={`settings-page-${value.id}`} accessibilityRole="button" accessibilityLabel={t(value.title)} accessibilityState={{selected: value.id === page.id}} onFocus={theme.isTV ? () => setSelected(value.id) : undefined} onPress={() => choose(value.id)} focusKind="row" focusRadius={14} style={{minHeight: theme.isTV ? 56 : compact ? 44 : 48, paddingVertical: compact ? 6 : undefined, paddingHorizontal: compact ? 14 : 20, gap: compact ? 12 : 18, borderRadius: 14, flexDirection: 'row', alignItems: 'center', backgroundColor: value.id === page.id && !compact ? `${theme.colors.accent}29` : 'transparent'}}>
    {theme.isTV && value.id === page.id && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 14, bottom: 14, width: 4, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
    <View style={compact ? {width: 30, height: 30, borderRadius: 8, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'} : undefined}><Icon name={nativeSettingsIcon(value.icon)} size={theme.isTV ? 24 : 18} color={theme.isTV ? value.id === page.id ? theme.colors.accentStrong : theme.colors.textTertiary : compact ? theme.colors.textSecondary : undefined}/></View><Text style={[shellType(theme.isTV ? 22 : 15.5), {color: theme.isTV && value.id !== page.id ? theme.colors.textSecondary : theme.colors.text, flex: 1}]}>{t(value.title)}</Text>{compact && <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>}
  </Focusable>;
  const [searching, setSearching] = useState(false), [searchRevision, setSearchRevision] = useState(0);
  const trackSearchFocus = theme.isTV && searching;
  useSyncExternalStore(trackSearchFocus ? registry.subscribe : noFocusSubscription, trackSearchFocus ? registry.getSnapshot : noFocusSnapshot, noFocusSnapshot);
  const voiceSearch = useTVVoiceSearch(theme.isTV && searching, controller.lifetime, setQuery, runtime.reportError);
  const [restoreRevision, setRestoreRevision] = useState(0);
  const liveControllers = useMemo(() => {
    if (!managesServer || theme.isTV) return undefined;
    const lifetime = controller.lifetime;
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => ports.context.api.request<T>(path, method, body, next), signal)};
    return {owner: new NativeLiveOwnerController(lifetime, api, () => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('')), recordings: createNativeRecordingsController(lifetime, api, ports.dvrRequestId)};
  }, [controller, ports, managesServer, theme.isTV]);
  useEffect(() => () => {liveControllers?.owner.dispose(); liveControllers?.recordings.dispose();}, [liveControllers]);
  const matches = searchSettings({headings: structure.headings.filter(value => value.id === 'account')}, query, t);
  const matchingPages = accountPages.flatMap(value => {
    const found = matches.filter(match => match.address === value.id);
    if (!found.length) return [];
    return [{...value, sections: value.sections.map(section => ({...section, advanced: false, rows: section.rows.filter(row => found.some(match => !match.rowId || match.rowId === settingsRowId(row)))})).filter(section => section.rows.length)}];
  });
  const serverPage = serverPages.find(value => value.id === selected);
  const page = serverPage ?? accountPages.find(value => value.id === selected) ?? accountPages[0];
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const column = ids.surface('settings.pages'), rows = ids.surface(`settings.rows.${page?.id ?? ''}`), keyboard = ids.surface('settings.keyboard'), searchField = ids.surface('settings.search');
  // Actual attached controls decide which result groups participate in the focus graph.
  // Conditional/unavailable rows must not strand focus in an empty intermediate group.
  const matchRegions = tvSettingsResultGroups(matchingPages.map(value => ids.surface(`settings.matches.${value.id}`)), registry.entryAddress);
  const matchEdges = (id: string) => tvGroupEdges(matchRegions, id, searchField);
  const enter = useScreenEntry(theme.isTV && page ? {regionId: column, childId: accountPages[0].id} : undefined, controller.lifetime.current);
  useDestinationEntryFocus({visible: theme.isTV && !searching, intent: restoreRevision ? 'enter' : 'restore', revision: restoreRevision}, page ? {regionId: column, childId: page.id} : undefined);
  const remoteSourcesPage = !theme.isTV && page?.id === 'remote-sources';
  const remoteBack = useRef<(() => boolean) | undefined>(undefined);
  const setRemoteBack = useCallback((handler: (() => boolean) | undefined) => {remoteBack.current = handler;}, []);
  const back = useCallback(() => {
    if (!viewCurrent()) return false;
    if (remoteSourcesPage) {
      if (remoteBack.current?.()) return true;
      pageState.current = {...pageState.current, pushed: false}; setPushed(false);
      if (theme.width >= 600 && accountPages[0]) {pageState.current = {...pageState.current, selected: accountPages[0].id}; setSelected(accountPages[0].id);}
      return true;
    }
    if (searching) {setSearching(false); setQuery(''); setRestoreRevision(value => value + 1); return true;}
    if (registry.capture()?.regionId === rows && page) return registry.focus({regionId: column, childId: page.id});
    if (page && page.id !== accountPages[0]?.id) {pageState.current = {...pageState.current, selected: accountPages[0].id}; setSelected(accountPages[0].id); registry.focus({regionId: column, childId: accountPages[0].id}); return true;}
    return false;
  }, [registry, column, rows, page, accountPages, searching, remoteSourcesPage, theme.width, viewCurrent]);
  const account = ports.context.account.account;
  const signedIn = account.hostedAccountId
    ? t('settings.signedInWithAccount', {server: ports.serverName()})
    : t('settings.signedInAs', {server: ports.serverName(), username: account.username});
  // The drawn sheet heads the profile, then the routes this build can really open.
  const unreadNotifications = shell.unreadNotificationCount ?? 0;
  const sheetShortcuts = shell.account.actions.filter(action => action.id !== 'settings').map(action => ({...action,
    onPress: () => {if (viewCurrent()) action.onPress();},
    ...(action.id === 'notifications' && unreadNotifications > 0 ? {trailing: <Text style={[shellType(11, 700), {color: theme.colors.ink}]}>{defaultI18n.number(unreadNotifications)}</Text>} : {}),
  }));
  // The compact sheet leaves through its own exit first; Back follows once it has gone.
  const [closing, setClosing] = useState(false), closingRef = useRef(false);
  const closeSettings = () => {
    if (!viewCurrent() || closingRef.current) return;
    if (!compact) {navigation.back(ports.context.authority()); return;}
    closingRef.current = true; setClosing(true);
  };
  const finishClose = () => {if (closingRef.current && viewCurrent()) navigation.back(ports.context.authority());};
  // A pushed Server page covers the shell chrome, as the frames draw it; phone only.
  useEffect(() => {
    setNativeScreenChromeHidden('settings-sheet', compact);
    return () => setNativeScreenChromeHidden('settings-sheet', false);
  }, [compact]);
  useScreenBack(runtime, back, theme.isTV || remoteSourcesPage, {kind: 'settings'});
  if (!page) throw new Error('native_settings_structure_empty');
  // Libraries and Live TV draw their own pages and own their scrolling; they are not
  // stacks of rows inside the generic Settings body.
  const librariesPage = !theme.isTV && page.id === 'libraries';
  const livePage = !theme.isTV && page.id === 'live' && liveControllers !== undefined;
  const troubleshootingPage = !theme.isTV && page.id === 'troubleshooting';
  const storagePage = !theme.isTV && page.id === 'storage';
  const integrationsPage = !theme.isTV && page.id === 'integrations';
  const generalPage = !theme.isTV && page.id === 'general';
  const streamingPage = !theme.isTV && page.id === 'streaming';
  const segmentsPage = !theme.isTV && page.id === 'segments';
  const peoplePage = !theme.isTV && page.id === 'people';
  const signInPage = !theme.isTV && page.id === 'signin';
  const metadataPage = !theme.isTV && page.id === 'metadata';
  const historyPage = !theme.isTV && page.id === 'history';
  const remoteAccessPage = !theme.isTV && page.id === 'remote';
  // Medium and expanded Settings draw the owner Dashboard as a Settings page; phone keeps
  // its dedicated pushed Dashboard route.
  const dashboardPage = !theme.isTV && !compact && page.id === 'dashboard' && dashboard !== undefined;
  const dashboardDenied = useCallback(() => {
    if (!viewCurrent()) return;
    const first = accountPages[0]?.id;
    if (first) {pageState.current = {...pageState.current, selected: first}; setSelected(first);}
  }, [accountPages, viewCurrent]);
  const leaveServerPage = () => {if (!viewCurrent()) return; const first = accountPages[0]?.id; if (first) {pageState.current = {...pageState.current, selected: first}; setSelected(first); registry.focus({regionId: column, childId: first});}};
  const choose = (id: string) => {if (!viewCurrent()) return; pageState.current = {...pageState.current, selected: id, pushed: !theme.isTV}; setSelected(id); if (!theme.isTV) setPushed(true);};
  const phoneBack = () => {if (!viewCurrent()) return; if (!remoteSourcesPage || !remoteBack.current?.()) popPage();};
  const viewerRole = ports.context.account.account.role === 'owner' ? 'owner' as const : ports.context.account.canManage ? 'administrator' as const : 'member' as const;
  // Android may report 0 here while the surface still draws under the status bar; StatusBar.currentHeight is the measured fallback.
  const topInset = Math.max(insets.top, Platform.OS === 'android' ? StatusBar.currentHeight ?? 0 : 0);
  const customPage = dashboardPage || librariesPage || livePage || troubleshootingPage || storagePage || integrationsPage || generalPage || streamingPage || segmentsPage || peoplePage || signInPage || metadataPage || historyPage || remoteSourcesPage || remoteAccessPage;
  const content = <Screen style={!theme.isTV ? {paddingTop: compact ? 0 : topInset + 14} : undefined}>
    <View style={{flex: 1, flexDirection: 'row', gap: theme.isTV ? 80 : 0}}>
      {(!compact || !pushed) && <View style={{width: compact ? '100%' : theme.isTV ? searching ? 500 : 480 : 280}}>
        {(theme.isTV ? !searching : !compact) && <Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : 30, 800), {color: theme.colors.text, marginBottom: 24, marginHorizontal: 20}]}>{t('next.shell.settings')}</Text>}
        <FocusRegion id={searchField} entry="input" edges={{left: ids.rail, down: searching ? `${keyboard}.row.0` : column}}>
          <TextField focusId="input" testID="settings-search" variant="search" leadingIcon="i-search" accessibilityLabel={t('next.shell.searchSettings')} placeholder={t('next.shell.searchSettings')} value={query} onChangeText={setQuery} showSoftInputOnFocus={!theme.isTV}
            onFocus={() => {if (theme.isTV && !searching) {setSearching(true); setSearchRevision(value => value + 1);}}} containerStyle={{marginHorizontal: theme.isTV ? 0 : 16}}/>
        </FocusRegion>
        {theme.isTV && searching ? <View style={{marginTop: 32}}><NativeTVKeyboard id={keyboard} value={query} onChangeText={setQuery} activation={{visible: true, intent: 'enter', revision: searchRevision}} leftRegionId={ids.rail} rightRegionId={matchRegions[0]} width={500} testID="settings-keyboard" onVoice={voiceSearch.start} voiceBusy={voiceSearch.busy}/></View> : <ScrollView style={{marginTop: 20, marginHorizontal: theme.isTV ? -16 : 0}} contentContainerStyle={theme.isTV ? {paddingHorizontal: 16} : {paddingBottom: 24 + insets.bottom}}>
          {query.trim().length >= 2 ? <SettingsSearchResults matches={matches} query={query} onNavigate={address => choose(address)}/>
            : <>
              {compact && <>
                <Focusable testID="settings-profile-card" accessibilityRole="button" accessibilityLabel={shell.profile.name} onPress={() => choose('profile')} focusKind="row" focusRadius={14} style={{marginHorizontal: 16, minHeight: 78, paddingVertical: 12, paddingHorizontal: 14, gap: 14, borderRadius: 14, flexDirection: 'row', alignItems: 'center', backgroundColor: theme.colors.raised}}>
                  <ProfileFace {...shell.profile} size={54} locked={shell.profile.pinRequired} scaleOnFocus={false}/>
                  <View style={{flex: 1, minWidth: 0}}>
                    <Text numberOfLines={1} style={[shellType(18, 800, 'normal', -.02), {color: theme.colors.text}]}>{shell.profile.name}</Text>
                    <Text numberOfLines={1} style={[shellType(13, 400, 17), {color: theme.colors.textTertiary, marginTop: 2}]}>{signedIn}</Text>
                  </View>
                  <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
                </Focusable>
                {sheetShortcuts.length > 0 && <ShortcutTiles testID="settings-shortcuts" actions={sheetShortcuts}/>}
              </>}
              <FocusRegion id={column} entry={orderedPages[0].id} order={orderedPages.map(value => value.id)} orientation="vertical" edges={{left: ids.rail, right: rows, up: searchField}} onLayout={enter}>
                <Text style={[shellType(theme.isTV ? 20 : 13, 600), {color: theme.colors.textTertiary, marginLeft: 20, marginBottom: 10}]}>{t('settings.heading.account')}</Text>
                {theme.isTV
                  ? tvPageGroups.map((group, index) => <React.Fragment key={group.join('-')}>
                      {index > 0 && <View style={{height: 26}}/>}
                      {group.flatMap(id => orderedPages.filter(value => value.id === id)).map(pageRow)}
                    </React.Fragment>)
                  : compact ? <View style={{marginHorizontal: 16, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{orderedPages.map((value, index) => <View key={value.id}>{pageRow(value)}{index > 0 && <View pointerEvents="none" style={{position: 'absolute', left: 56, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}</View>)}</View>
                    : grouped(orderedPages, tabletAccountClusters).map((group, index) => <React.Fragment key={group[0]?.id ?? index}>
                        {index > 0 && <View style={{height: 12}}/>}
                        {group.map(pageRow)}
                      </React.Fragment>)}
              </FocusRegion>
            </>}
            {theme.isTV && !searching && <View testID="settings-key-hints" style={{gap: 8, marginTop: 34, marginLeft: 20}}>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}><KeyCap label={t('next.controls.native.up')}/><KeyCap label={t('next.controls.native.down')}/><Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.shell.settingsHintChoose')}</Text></View>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}><KeyCap label={t('next.controls.native.right')}/><Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.shell.settingsHintEdit')}</Text><View style={{width: 12}}/><KeyCap label={t('next.controls.native.left')}/><Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.shell.settingsHintPages')}</Text></View>
            </View>}
          {!theme.isTV && serverPages.length > 0 && <View style={{marginHorizontal: 16, marginTop: 20, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .28)}}>
            <ServerHeader serverName={ports.serverName()} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone"/>
            {grouped(serverPages, compact ? [] : tabletServerClusters).map((group, index) => <React.Fragment key={group[0]?.id ?? index}>
              {index > 0 && <View style={{height: 10}}/>}
              {group.map(value => <Focusable key={value.id} focusId={value.id} testID={`settings-page-${value.id}`} accessibilityRole="button" accessibilityLabel={t(value.title)} accessibilityState={{selected: value.id === page.id}} onPress={() => {if (!viewCurrent()) return; if (value.id === 'dashboard' && (dashboard === undefined || compact)) navigation.open({kind: 'server-dashboard'}, ports.context.authority()); else choose(value.id);}} focusKind="row" focusRadius={10} style={{minHeight: 48, paddingHorizontal: 14, gap: 12, flexDirection: 'row', alignItems: 'center', backgroundColor: value.id === page.id && !compact ? alpha(theme.colors.accent, .16) : 'transparent'}}>
                <View style={{width: 30, height: 30, borderRadius: 8, backgroundColor: alpha(theme.colors.accent, .16), alignItems: 'center', justifyContent: 'center'}}><Icon name={nativeSettingsIcon(value.icon)} size={18} color={theme.colors.accentStrong}/></View>
                <Text numberOfLines={1} style={[shellType(15.5), {color: theme.colors.text, flex: 1}]}>{t(value.title)}</Text><Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
              </Focusable>)}
            </React.Fragment>)}
          </View>}
        </ScrollView>}
      </View>}
      {(!compact || pushed) && <View style={{flex: 1, ...(!compact && !theme.isTV && !customPage ? {paddingTop: 46, paddingHorizontal: theme.width < 1024 ? 24 : 32, paddingBottom: 40} : {})}}>
        {compact && <PhoneServerPageHeader title={t(page.title)} backLabel={t('next.shell.settings')} onBack={phoneBack}
          band={serverPage ? <ServerBand viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} height={56} style={{position: 'absolute', inset: 0}}/> : undefined}/>}
        {!compact && !dashboardPage && !(theme.isTV && searching) && !customPage && <Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : 40, 800), {color: theme.colors.text, marginBottom: 28, marginHorizontal: compact ? 20 : 0}]}>{t(page.title)}</Text>}
        {state.error !== undefined && <StateView {...runtime.error(state.error, () => {void controller.refresh();}, 'settings')}/>}
        {historyPage ? <NativeServerHistory ports={ports} runtime={runtime} compactHeader={compact}/> : peoplePage ? <NativeServerPeople ports={ports} runtime={runtime} compactHeader={compact}/> : signInPage ? <NativeServerSignIn ports={ports} runtime={runtime} compactHeader={compact} onOpenPage={choose}/> : metadataPage ? <NativeServerMetadata ports={ports} runtime={runtime} compactHeader={compact} onLibraries={() => choose('libraries')}/> : dashboardPage ? <NativeDashboardScreen controller={dashboard!} ports={ports} runtime={runtime} inline onDenied={dashboardDenied} deniedFallback={null}/> : remoteAccessPage ? <NativeRemoteAccessPage ports={ports} runtime={runtime} compactHeader={compact}/> : segmentsPage ? <NativeServerSegments ports={ports} runtime={runtime}/> : remoteSourcesPage ? <NativeRemoteSourcesScreen ports={ports} runtime={runtime} onBackHandlerChange={setRemoteBack}/> : livePage ? <NativeLiveOwnerScreen controller={liveControllers!.owner} recordings={liveControllers!.recordings} ports={ports} runtime={runtime} onClose={leaveServerPage} initialChannelId={initialChannelId}/> : librariesPage ? <NativeOwnerLibrariesPanel ports={ports} runtime={runtime}/> : troubleshootingPage ? <NativeServerTroubleshooting ports={ports} runtime={runtime} compactHeader={compact}/> : storagePage ? <NativeServerStorage ports={ports} runtime={runtime} compactHeader={compact} onOpenPage={choose}/> : integrationsPage ? <NativeServerIntegrations ports={ports} runtime={runtime} compactHeader={compact}/> : streamingPage ? <NativeServerStreaming ports={ports} runtime={runtime} compactHeader={compact}/> : generalPage ? <NativeServerGeneral ports={ports} runtime={runtime} compactHeader={compact}/> : <FormScrollView key={page.id} style={theme.isTV ? {marginHorizontal: -24, marginTop: -16} : undefined} contentContainerStyle={theme.isTV ? {paddingHorizontal: 24, paddingTop: 16, paddingBottom: 80} : {paddingHorizontal: compact ? 20 : 0, paddingBottom: 80}} keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive">
          {theme.isTV && searching ? <>
            {query.trim().length >= 2 && matches.length > 0 && <Text testID="settings-search-count" style={[shellType(20, 600), {color: theme.colors.textTertiary, marginBottom: 18}]}>{t('next.shell.tvSettingsMatchCount', {count: matches.length})}</Text>}
            {query.trim().length >= 2 && !matches.length && <StateView title={t('next.states.noMatches', {query: query.trim()})}/>}
            {matchingPages.map(value => <View key={value.id}><Text accessibilityRole="header" style={[shellType(32, 800), {color: theme.colors.text, marginBottom: 24}]}>{t(value.title)}</Text><SettingsPageBody reportedOutputs={reportedOutputs} awayLimitLabel={awayLimitLabel} onStreaming={() => choose('streaming')} onQuality={() => choose('quality')} page={value} ports={ports} controller={controller} runtime={runtime} devicePreferences={devicePreferences} region={ids.surface(`settings.matches.${value.id}`)} column={keyboard} verticalEdges={matchEdges(ids.surface(`settings.matches.${value.id}`))}/></View>)}
            <Text testID="settings-search-help" style={[shellType(18, 400), {color: theme.colors.textTertiary, marginTop: 24}]}>{t('next.shell.tvSettingsSearchHelp')}</Text>
          </> : <SettingsPageBody reportedOutputs={reportedOutputs} awayLimitLabel={awayLimitLabel} onStreaming={() => choose('streaming')} onQuality={() => choose('quality')} page={page} ports={ports} controller={controller} runtime={runtime} devicePreferences={devicePreferences} region={rows} column={column}/>}
        </FormScrollView>}
      </View>}
    </View>
  </Screen>;
  return !theme.isTV ? <SettingsSheet open={!closing} onExited={finishClose} host="inline" presentation={compact ? 'sheet' : 'page'} profile={null} shortcuts={null} scrollable={false} testID="settings-sheet"
    onOpenChange={(open, reason) => {
      if (open || !viewCurrent() || closingRef.current) return;
      if (pushed && (reason === 'back' || reason === 'escape')) {
        phoneBack();
        return;
      }
      closeSettings();
    }} headerContent={pushed ? <View/> : <View testID="settings-sheet-header" style={{height: 33, paddingHorizontal: 16, justifyContent: 'center'}}>
      <Text accessibilityRole="header" pointerEvents="none" style={[shellType(17, 700), {position: 'absolute', left: 64, right: 64, textAlign: 'center', color: theme.colors.text}]}>{t('next.shell.settings')}</Text>
      <Button testID="settings-sheet-done" variant="text" size="small" onPress={closeSettings} style={{alignSelf: 'flex-end', height: 32, paddingHorizontal: 0}} textStyle={shellType(16, 700)}>{t('action.done')}</Button>
    </View>}><View style={{flex: 1}} pointerEvents={closing ? 'none' : 'box-none'}>{content}</View></SettingsSheet> : content;
}

/** playback-info frame 13b draws one hint under each Player choice; the runtime settings
 * structure has no hint field yet, so the drawn copy is attached by the one device row that
 * uses it. The labels remain the spec's own. */
const playerEngineHints: Readonly<Record<string, MessageId>> = Object.freeze({
  automatic: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.hint',
  native: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.hint',
  mpv: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.hint',
});
/** One Player choice opening: the exact page, route snapshot, controller and device-preference
 * source that offered it, with the choices that were on screen. Every select and close proves
 * this identity, so no retained opener or late item can write or close a successor. */
type EngineChooser = Readonly<{
  token: number; page: string; revision: number;
  route: unknown; controller: unknown; preferences: unknown; mount: object;
  value: string;
  choices: readonly Readonly<{value: string | number | boolean; label: MessageId; labelValues?: unknown}>[];
  options: readonly Readonly<{value: string; label: string; description?: string}>[];
  apply: (value: string) => void;
}>;
type EngineWitness = Readonly<{page: string; controller: unknown; preferences: unknown; mount: object; route: unknown}>;
/** The exact committed player-chooser owner. The opening and its `engineRef` identity are checked
 * first; only once those pass are the published witness, this mount and the live route observed —
 * `navigation.getSnapshot()` is called here, on the actual navigation, at the original position. */
function engineOpeningAlive(
  opening: EngineChooser | undefined,
  engineRef: Readonly<{current: EngineChooser | undefined}>,
  witnesses: Readonly<{live: Readonly<{current: EngineWitness | undefined}>; mounted: Readonly<{current: boolean}>}>,
  controller: NativeSettingsController,
  navigation: NativeSettingsLivePorts['context']['navigation'],
): boolean {
  if (!opening || engineRef.current !== opening) return false;
  const live = witnesses.live.current;
  return witnesses.mounted.current && !!live && live.page === opening.page && live.controller === opening.controller
    && live.preferences === opening.preferences && live.mount === opening.mount && live.route === opening.route
    && opening.route === navigation.getSnapshot() && controller.lifetime.current();
}
function SettingsPageBody({page, ports, controller, runtime, devicePreferences, region, column, reportedOutputs, awayLimitLabel, onStreaming, onQuality, verticalEdges}: {verticalEdges?: {up?: string; down?: string}; reportedOutputs?: Readonly<{hdr: boolean; surround: boolean}>; awayLimitLabel?: string; onStreaming: () => void; onQuality: () => void; page: SettingsPage; ports: NativeSettingsLivePorts; controller: NativeSettingsController; runtime: NativeShellRuntime; devicePreferences: NativeDevicePreferences; region: string; column: string}) {
  const theme = useTheme(), t = runtime.text.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [advanced, setAdvanced] = useState(false);
  // The public browser handoff does not grant native password/two-step editing.
  const settingsCapabilities = ports.settingsContext().capabilities;
  const offersWebSignIn = !!settingsCapabilities.localAccount && !settingsCapabilities.porticoMember;
  const managementURL = (() => {
    if (!(theme.isTV && page.id === 'profile' && offersWebSignIn)) return undefined;
    try {return new URL('/settings/profile', ports.context.api.baseUrl).href;} catch {return undefined;}
  })();
  // Frame 23: a TV row with a value opens its choices in the side sheet; switches still flip in place.
  const [choices, setChoices] = useState<Readonly<{title: string; help?: string; value: string; options: readonly Readonly<{value: string; label: string; description?: string}>[]; apply: (value: string) => void}>>();
  const [languages, setLanguages] = useState<Readonly<{testID: string; title: string; help?: string; value: readonly LanguageOption[]; available: readonly LanguageOption[]; limit: number; apply: (value: readonly LanguageOption[]) => void}>>();
  type AccountConfirm = Readonly<{kind: 'signOut' | 'resetRecommendations'; token: number; page: string; revision: number; controller: typeof controller}>;
  const [confirmOpen, setConfirmOpen] = useState<AccountConfirm>(), [actionBusy, setActionBusy] = useState(false), [actionError, setActionError] = useState<unknown>();
  // The confirmation belongs to one opening and one rendered page/route; the flight holds that
  // same object, so no delayed close, result or busy release can reach a successor. Refs are
  // explicit about their empty state, as the current React types require.
  const navigation = ports.context.navigation;
  const route = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const confirmRef = useRef<AccountConfirm | undefined>(undefined), confirmFlight = useRef<AccountConfirm | undefined>(undefined), confirmToken = useRef(0), confirmScope = useRef<string | undefined>(undefined), confirmOwner = useRef(controller);
  const scope = `${page.id}|${route.revision}`;
  if (confirmScope.current !== scope || confirmOwner.current !== controller) {confirmScope.current = scope; confirmOwner.current = controller; confirmRef.current = undefined; confirmFlight.current = undefined;}
  const shown = confirmOpen && confirmRef.current === confirmOpen && confirmScope.current === scope && confirmOpen.controller === controller
    && confirmOpen.page === page.id && confirmOpen.revision === route.revision ? confirmOpen : undefined;
  useLayoutEffect(() => () => {confirmRef.current = undefined; confirmFlight.current = undefined;}, []);
  // The Player chooser is one owned opening of one committed page, source and mount. The owner
  // is published in the layout turn; a retained opener, select or close is refused when any part
  // of it moved. The mount witness is replaced after a cleanup so a replayed setup revives nothing.
  const [engineMount, setEngineMount] = useState<object>(() => ({}));
  const [engineOpen, setEngineOpen] = useState<EngineChooser>();
  const engineRef = useRef<EngineChooser | undefined>(undefined), engineToken = useRef(0);
  const engineMounted = useRef(true), engineRetired = useRef(false);
  const engineLive = useRef<EngineWitness | undefined>(undefined);
  // The committed owner is published in the layout turn, never during render: an abandoned
  // render cannot replace the witness, and the cleanup clears only the owner it published.
  useLayoutEffect(() => {
    const owner = {page: page.id, controller, preferences: devicePreferences, mount: engineMount, route};
    engineLive.current = owner;
    return () => {if (engineLive.current === owner) engineLive.current = undefined;};
  }, [page.id, controller, devicePreferences, engineMount, route]);
  const engineAlive = (opening: EngineChooser | undefined) => engineOpeningAlive(opening, engineRef, {live: engineLive, mounted: engineMounted}, controller, navigation);
  const engineShown = engineOpen && !state.busy && engineAlive(engineOpen) ? engineOpen : undefined;
  const retireEngine = () => {engineRef.current = undefined; setEngineOpen(undefined);};
  const closeEngine = (opening: EngineChooser) => {if (!engineAlive(opening)) return; retireEngine();};
  useLayoutEffect(() => {
    // A replay's setup publishes a fresh witness after its cleanup retired the old one; the
    // first setup keeps the initializer so the rows drawn in the first render still work.
    engineMounted.current = true;
    if (engineRetired.current) {engineRetired.current = false; setEngineMount({});}
    return () => {engineRetired.current = true; engineMounted.current = false; engineRef.current = undefined;};
  }, []);
  // A page, source, route or unmount change retires the exact opening in the same layout turn.
  useLayoutEffect(() => {
    if (!engineOpen) return;
    if (!state.busy && engineOpeningAlive(engineOpen, engineRef, {live: engineLive, mounted: engineMounted}, controller, navigation)) return;
    retireEngine();
  }, [engineOpen, state.busy, page.id, controller, devicePreferences, route, navigation]);
  const openConfirm = (kind: AccountConfirm['kind']) => {
    // A delayed row action from an old page may not reopen its modal on the successor page.
    if (confirmScope.current !== scope || confirmOwner.current !== controller) return;
    const next: AccountConfirm = {kind, token: ++confirmToken.current, page: page.id, revision: route.revision, controller};
    confirmFlight.current = undefined; confirmRef.current = next; setActionBusy(false); setActionError(undefined); setConfirmOpen(next);
  };
  const closeConfirm = (target: AccountConfirm) => {if (confirmRef.current !== target) return; confirmRef.current = undefined; setConfirmOpen(current => current === target ? undefined : current);};
  const cancel = useRef<View>(null), firstLanguage = useRef<View>(null);
  useSyncExternalStore(devicePreferences.subscribe, () => page.sections.flatMap(section => section.rows).filter(row => row.kind === 'composite' && row.storage === 'device').map(row => devicePreferences.read((row as Extract<Row, {kind: 'composite'}>).id)).join('|'));
  useEffect(() => setAdvanced(false), [page.id]);
  const visible = (row: Row) => !('when' in row) || !row.when || state.preferences.effective[row.when.key] === ('equals' in row.when ? row.when.equals : undefined);
  const enabled = (row: Row) => {
    if (!('enableWhen' in row) || !row.enableWhen) return true;
    const value = state.preferences.effective[row.enableWhen.key];
    return value !== undefined && row.enableWhen.in.includes(value);
  };
  const engine = useSyncExternalStore(devicePreferences.subscribe, () => devicePreferences.read('playerEngine'));
  const hasDevicePlayer = Platform.OS === 'android' && page.id === 'device' && engine !== undefined;
  // The drawn Player group sits before Connection (settings-spec advanced group 4)
  // and before Playback fixes. The section id is the source anchor; keep the appended
  // fallback when the page does not carry it.
  const playerSection: SettingsPage['sections'][number] = {
    id: 'device-player',
    advanced: true,
    title: 'next.settingsSpec.account.device.advanced.groups.3.title',
    rows: [{
      kind: 'composite',
      id: 'playerEngine',
      storage: 'device',
      scope: 'device',
      label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.label',
      control: {
        type: 'choice',
        choices: [
          {value: 'automatic', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.label'},
          {value: 'native', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.label'},
          {value: 'mpv', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.label'},
        ],
      },
    },
    // Frame 11 draws Playback fixes on television only; Android touch keeps the Player row alone.
    ...(theme.isTV ? [{
      kind: 'action' as const,
      id: 'devicePlayerFixes' as const,
      label: 'next.settingsSpec.account.device.advanced.groups.3.rows.1.label' as const,
      value: 'next.settingsSpec.account.device.advanced.groups.3.rows.1.control.label' as const,
    }] : []),
    ],
  };
  const connectionAt = page.sections.findIndex(section => section.id === 'deviceConnection');
  // Television alone moves the injected group before Connection; a phone or tablet keeps
  // the previous appended placement.
  const pageSections: SettingsPage['sections'] = hasDevicePlayer
    ? theme.isTV && connectionAt >= 0
      ? [...page.sections.slice(0, connectionAt), playerSection, ...page.sections.slice(connectionAt)]
      : [...page.sections, playerSection]
    : page.sections;
  // Frame 12: Apple TV's Advanced holds Capabilities, Connection, Storage and Support, so the
  // Connection group (with Insecure connections, P2-69) is on every native platform.
  const sections = pageSections.filter(section => !section.advanced || advanced);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const showEqualizer = page.id === 'music' && equalizer.supported;
  useEffect(() => {
    if (showEqualizer && !equalizer.ready && !nativeEqualizer.getSnapshot().pending) void nativeEqualizer.refresh().catch(runtime.reportError);
  }, [showEqualizer, equalizer.ready, runtime.reportError]);
  const showSafeMode = theme.isTV && page.id === 'quality' && nativeSafeModeAvailable(devicePreferences);
  const showDynamicRange = theme.isTV && nativeDynamicRangeMatchingAvailable();
  const rowIds = sections.flatMap(section => [...section.rows.filter(visible).map(settingsRowId).filter(id => id !== 'equalizer'),
    ...(showEqualizer && section.id === 'sound' ? ['settings-equalizer'] : []),
    ...(section.id === 'limits' && awayLimitLabel ? ['settings-away-limit', ...(!theme.isTV ? ['settings-away-limit-change'] : [])] : []),
    ...(theme.isTV && Platform.OS === 'android' && page.id === 'device' && advanced && section.id === 'about' ? passthroughFormats.map(format => `settings-passthrough-${format}`) : []),
    ...(theme.isTV && page.id === 'device' && section.id === 'device' ? [...(Platform.OS === 'android' ? [nativeFrameRateFocusId, nativePassthroughFocusId] : []), ...(showDynamicRange ? [nativeDynamicRangeFocusId] : []), nativeLossNoticesFocusId] : [])]);
  // A custom panel hosts its own rows under ids that are not these (settings-panel-profiles for
  // 'profiles'), so naming rowIds[0] as the entry finds no host and the entry falls through to the
  // only id that does match: the last row. With panels the region enters at its first mounted row.
  const hostedRows = !sections.some(section => section.rows.some(row => row.kind === 'custom' && visible(row)));
  const skipMatrix = !theme.isTV && theme.platform !== 'phone';
  const change = (row: Extract<Row, {kind: 'preference' | 'composite'}>, value: string | number | boolean | readonly string[]) => {
    if (!enabled(row)) return;
    if (row.kind === 'composite' && row.storage === 'device') {void devicePreferences.write(row.id, value as string | boolean).catch(runtime.reportError); return;}
    const patch = row.kind === 'preference' ? {[row.key]: value} : compositePatch(row.id, value as string | boolean);
    const field = state.preferences.registry.fields.find(value => value.key === Object.keys(patch)[0]);
    if (!field) return;
    const scope: PreferenceScope | undefined = row.scope === 'deviceClass' ? field.scopes.find(value => value === 'profile-device-class') : field.scopes.find(value => value === 'profile-server');
    if (!scope) throw new Error('native_settings_scope_unavailable');
    void controller.apply(scope, patch).catch(runtime.reportError);
  };
  if (showSafeMode) rowIds.unshift(nativeSafeModeFocusId);
  if (pageSections.some(section => section.advanced)) rowIds.push('settings-advanced');
  const device = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  return <><FocusRegion id={region} entry={hostedRows ? rowIds[0] : undefined} entryPolicy="entry" order={hostedRows ? rowIds : undefined} orientation="vertical" edges={{left: column, ...verticalEdges}}>
    {actionError !== undefined && <StateView {...runtime.error(actionError, () => setActionError(undefined), 'settings')}/>}
    {showSafeMode && <NativeSafeModeRow source={devicePreferences} runtime={runtime} current={controller.lifetime.current}/>}
    {sections.map(section => <React.Fragment key={section.id}><View style={{marginBottom: theme.isTV ? 30 : 24}}>
      {section.title && <Text style={[shellType(theme.isTV ? 20 : 18, 700), {color: theme.colors.textTertiary, marginLeft: theme.isTV ? 24 : 0, marginBottom: 10}]}>{t(section.title)}</Text>}
      {section.description && <Text style={[shellType(theme.isTV ? 22 : 14), {color: theme.colors.textSecondary, marginBottom: 14}]}>{t(section.description)}</Text>}
      <View style={{backgroundColor: theme.colors.raised, borderRadius: theme.isTV ? 18 : 14}}>{section.rows.filter(visible).map((row, index) => {
        const id = settingsRowId(row);
        // Frame 12's Apple passthrough is the rows.7 capability line, not the rows.6
        // receiver choice (same visible label, distinct key). The Apple engines have no
        // stored passthrough control, so the choice is suppressed and only the rows.7 row
        // is replaced by the read-only eligibility line.
        if (theme.isTV && Platform.OS === 'ios' && page.id === 'device') {
          if (row.label === 'next.settingsSpec.account.device.groups.1.rows.6.label') return null;
          if (row.label === 'next.settingsSpec.account.device.groups.1.rows.7.label') return <NativeApplePassthroughRow key={id} testID={id}/>;
        }
        if (row.kind === 'custom') return <React.Fragment key={id}>{ports.renderSettingsPanel(row.id, runtime)}</React.Fragment>;
        // MOB-023: on tablet touch layouts Intro/Credits/Recap skipping is one choice matrix.
        if (skipMatrix && row.kind === 'preference' && SKIP_KEYS.includes(row.key)) {
          if (row.key !== SKIP_KEYS[0]) return null;
          const skipRows = section.rows.filter(visible).filter((value): value is Extract<Row, {kind: 'preference'}> => value.kind === 'preference' && SKIP_KEYS.includes(value.key) && state.preferences.effective[value.key] !== undefined);
          const choices = row.control.type === 'choice' ? row.control.choices : [];
          if (!skipRows.length || !choices.length) return null;
          return <ChoiceMatrix key={id} testID="settings-skipping" rows={skipRows.map(value => ({id: value.key, label: t(value.label), value: String(state.preferences.effective[value.key])}))}
            options={choices.map(choice => ({value: String(choice.value), label: t(choice.label, choice.labelValues), disabled: state.busy}))}
            onChange={(key, next) => {const target = skipRows.find(value => value.key === key); const choice = choices.find(value => String(value.value) === next); if (target && choice) change(target, choice.value);}}/>;
        }
        if (row.kind === 'server-setting') throw new Error('native_account_page_server_row');
        if (row.kind === 'action') {
          const target = nativeSettingsActionTarget(row.id);
          return <SettingsRow key={id} testID={id} destructive={row.destructive} label={t(row.label)} description={row.help ? t(row.help) : undefined}
            value={row.value ? t(row.value) : undefined} first={index === 0} onPress={() => {
            controller.lifetime.assert();
            if (target === 'linkTV') ports.accountActions.linkTV();
            else if (target === 'customizeHome') ports.context.navigation.open({kind: 'home-customize'}, ports.context.authority());
            else if (target === 'quality') onQuality();
            else openConfirm(row.id === 'signOut' ? 'signOut' : 'resetRecommendations');
          }}/>;
        }
        let value = row.kind === 'preference' ? state.preferences.effective[row.key] : row.storage === 'registry' ? readComposite(row.id, key => state.preferences.effective[key]) : devicePreferences.read(row.id);
        // No override stored: the drawn TV choice shows what the set reports. Both
        // facts must be known, or the row stays out rather than guessing one.
        if (value === undefined && row.kind === 'composite' && row.id === 'tvSupports' && reportedOutputs)
          value = reportedOutputs.hdr ? reportedOutputs.surround ? 'hdr-surround' : 'hdr' : reportedOutputs.surround ? 'surround' : 'none';
        if (value === undefined) return null;
        const scope = settingsScopeLabel(row.scope, theme.isTV ? 'tv' : 'phone');
        const copy = {label: t(row.label), description: row.help ? t(row.help) : undefined, scope: scope ? t(scope) : undefined, first: index === 0, dependent: !!row.when || !!row.enableWhen};
        const control = row.control;
        if (theme.isTV && control.type !== 'switch') {
          if (control.type === 'languages') {
            const selected = (Array.isArray(value) ? value : []).map(language => ({id: language, label: languageName(language, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}));
            const available = COMMON_LANGUAGES.map(language => ({id: language, label: languageName(language, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}));
            return <SettingsRow key={id} testID={id} {...copy} value={selected.length ? defaultI18n.list(selected.map(language => language.label)) : t('next.shell.native.noLanguages')}
              onPress={() => setLanguages({testID: id, title: copy.label, help: copy.description, value: selected, available, limit: MAX_LANGUAGES, apply: next => change(row, next.map(language => language.id))})}/>;
          }
          const options = control.type === 'choice' ? control.choices.map(choice => {
            const hint = id === 'playerEngine' ? playerEngineHints[String(choice.value)] : undefined;
            return {value: String(choice.value), label: t(choice.label, choice.labelValues), description: hint ? t(hint) : undefined};
          }) : localeChoices(defaultI18n, String(value)).map(choice => ({value: choice.id, label: choice.label}));
          const current = String(value);
          const currentLabel = options.find(option => option.value === current)?.label;
          return <SettingsRow key={id} testID={id} {...copy} value={currentLabel}
            onPress={() => setChoices({title: copy.label, help: copy.description, value: current, options, apply: next => {
              if (control.type === 'choice') {const choice = control.choices.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);}
              else change(row, next);
            }})}/>;
        }
        if (control.type === 'choice' && id === 'playerEngine') {
          const offered = control.choices;
          const options = offered.map(choice => {
            const hint = playerEngineHints[String(choice.value)];
            return {value: String(choice.value), label: t(choice.label, choice.labelValues), description: hint ? t(hint) : undefined};
          });
          const owner = {page: page.id, controller, preferences: devicePreferences, mount: engineMount, route};
          return <SettingsRow key={id} testID={id} {...copy} value={options.find(option => option.value === String(value))?.label}
            style={state.busy ? {opacity: .5} : undefined} onPress={state.busy ? undefined : () => {
              // Prove the committed page, source, mount and route before storing the opening;
              // never a render-side owner or the latest refs. A busy controller refuses too.
              const live = engineLive.current;
              if (controller.getSnapshot().busy || !engineMounted.current || !live || live.page !== owner.page || live.controller !== owner.controller
                || live.preferences !== owner.preferences || live.mount !== owner.mount || live.route !== owner.route
                || owner.route !== navigation.getSnapshot() || !controller.lifetime.current()) return;
              const opening: EngineChooser = {token: ++engineToken.current, ...owner, revision: owner.route.revision,
                value: String(value), choices: offered, options,
                apply: next => {
                  const held = engineLive.current;
                  if (!engineMounted.current || !held || held.mount !== owner.mount || held.page !== owner.page
                    || held.controller !== owner.controller || held.preferences !== owner.preferences || held.route !== owner.route
                    || !controller.lifetime.current()) return;
                  const choice = offered.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);
                }};
              engineRef.current = opening;
              setEngineOpen(opening);
            }}/>;
        }
        const choiceList = control.type === 'choice' ? control.choices : undefined;
        if (theme.isTV && control.type === 'switch') {
          const on = value === true, blocked = state.busy || !enabled(row);
          // The whole row is the stop and OK flips it; the switch is drawn, never focused.
          return <SettingsRow key={id} testID={id} {...copy} readable={blocked} toggle={{checked: on, visual: <Switch checked={on} focusable={false} accessibilityLabel={t(row.label)} disabled={blocked}/>}}
            onPress={blocked ? undefined : () => change(row, !on)}/>;
        }
        return <SettingsRow key={id} testID={id} {...copy}
          control={control.type === 'switch' ? <Switch testID={id} checked={value === true} accessibilityLabel={t(row.label)} disabled={state.busy || !enabled(row)} onCheckedChange={next => change(row, next)}/>
            : choiceList ? (() => {const options = choiceList.map(choice => ({value: String(choice.value), label: t(choice.label, choice.labelValues)})); const selected = options.some(option => option.value === String(value)) ? String(value) : undefined; return <Select testID={id} accessibilityLabel={t(row.label)} value={selected} options={options} onValueChange={next => {const choice = choiceList.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);}} disabled={state.busy}/>;})()
              : control.type === 'languages' ? <LanguageList testID={id} label={t(row.label)} value={(Array.isArray(value) ? value : []).map(id => ({id, label: languageName(id, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}))} available={COMMON_LANGUAGES.map(id => ({id, label: languageName(id, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}))} limit={MAX_LANGUAGES} onChange={next => change(row, next.map(value => value.id))}/>
                : <Select testID={id} accessibilityLabel={t(row.label)} value={String(value)} options={localeChoices(defaultI18n, String(value)).map(choice => ({value: choice.id, label: choice.label}))} onValueChange={next => change(row, next)} disabled={state.busy}/>}/>;
      })}
      {section.id === 'limits' && awayLimitLabel ? <>
        <SettingsRow testID="settings-away-limit" label={t('next.settingsSpec.account.quality.groups.0.rows.3.label')} description={t('next.settingsSpec.account.quality.groups.0.rows.3.help')} value={awayLimitLabel}/>
        {!theme.isTV && <SettingsRow testID="settings-away-limit-change" label={t('next.settingsSpec.account.quality.groups.0.rows.3.children.0.label')} onPress={onStreaming}/>}
      </> : null}</View>
    </View>
    {theme.isTV && page.id === 'device' && section.id === 'device' && (
      <>
        {Platform.OS === 'android' && (
          <>
            <NativeFrameRateRow context={ports.context} runtime={runtime}/>
            <NativePassthroughRow context={ports.context} runtime={runtime}/>
          </>
        )}
        {showDynamicRange && <NativeDynamicRangeRow context={ports.context} runtime={runtime}/>}
        <NativeLossNoticesRow context={ports.context} runtime={runtime}/>
      </>
    )}
    {theme.isTV && Platform.OS === 'android' && page.id === 'device' && advanced && section.id === 'about' && (
      <>
        <NativeCapabilityRows/>
        <View style={{gap: 8, marginBottom: 30}}>
          <Text style={[shellType(20, 700), {color: theme.colors.textTertiary}]}>
            {t('next.settingsSpec.account.device.advanced.groups.1.title')}
          </Text>
          {passthroughFormats.map(format => (
            <NativePassthroughRow key={format} format={format} context={ports.context} runtime={runtime}/>
          ))}
        </View>
      </>
    )}
    {managementURL && section.id === 'who' && <ManagementCard url={managementURL} text={runtime.text}/>}</React.Fragment>)}
    {pageSections.some(section => section.advanced) && <Button testID="settings-advanced" variant="glass" size="small" trailingIcon={advanced ? 'i-up' : 'i-down'} onPress={() => setAdvanced(value => !value)}>{t('settings.section.advanced')}</Button>}
  </FocusRegion>
    <Dialog testID="settings-action-confirm" open={shown !== undefined} onOpenChange={open => {if (!open && !confirmFlight.current && shown) closeConfirm(shown);}} initialFocusRef={cancel}
      title={shown?.kind === 'signOut' ? t('settings.signOutConfirm.title', {server: ports.serverName(), device: t(device)}) : t('settings.resetRecommendations.confirmTitle')}
      description={t(shown?.kind === 'signOut' ? theme.isTV ? 'settings.signOutConfirm.bodyTV' : 'settings.signOutConfirm.bodyDownloads' : 'settings.resetRecommendations.confirmBody')}
      footer={<View style={{flexDirection: 'row', gap: 16}}><Button focusId="cancel" ref={cancel} variant="glass" disabled={actionBusy} onPress={() => {if (shown) closeConfirm(shown);}}>{t('action.cancel')}</Button><Button focusId="confirm" variant="glass" textStyle={{color: theme.colors.record}} loading={actionBusy} onPress={() => {
        const opening = shown;
        if (!opening || confirmFlight.current || confirmRef.current !== opening) return;
        // Re-read the route and visibility at the press, not the render that drew the dialog.
        const nav = navigation.getSnapshot();
        if (nav.revision !== opening.revision || !navigation.isCurrent()) return;
        if (runtime.getSnapshot().contentVisible !== true) return;
        if (opening.page !== page.id || opening.controller !== controller || !controller.lifetime.current()) return;
        confirmFlight.current = opening; setActionBusy(true);
        const work = opening.kind === 'signOut' ? ports.accountActions.signOut() : controller.lifetime.request(signal => resetRecommendations(ports.context.api, {serverId: controller.lifetime.authority.serverId, operationId: ports.requestId(), signal}));
        void work.then(
          // Only the opening that started this closes its own dialog; sign-out's own session
          // retirement is intentional, so its late UI writes are skipped rather than retried.
          () => {if (controller.lifetime.current() && confirmRef.current === opening) closeConfirm(opening);},
          error => {if (controller.lifetime.current() && confirmFlight.current === opening) setActionError(error);}
        ).finally(() => {
          if (confirmFlight.current !== opening) return;
          confirmFlight.current = undefined;
          if (!controller.lifetime.current()) return;
          setActionBusy(false);
        });
      }}>{t(shown?.kind === 'signOut' ? 'action.signOut' : 'settings.resetRecommendations.confirm')}</Button></View>}><View/></Dialog>
    {theme.isTV && <Menu testID="settings-value-sheet" open={choices !== undefined} onOpenChange={open => {if (!open) setChoices(undefined);}} presentation="sideSheet"
      title={choices?.title} description={choices?.help} initialFocusId={choices?.value}
      items={choices ? choices.options.map(option => ({id: option.value, label: option.label, description: option.description, selected: option.value === choices.value, onSelect: () => {const apply = choices.apply; setChoices(undefined); apply(option.value);}})) : []}/>}
    {engineShown && <Menu testID="settings-player-engine-sheet" open onOpenChange={open => {if (!open && engineShown) closeEngine(engineShown);}} presentation="sheet"
      title={t('next.settingsSpec.account.device.advanced.groups.3.rows.0.label')} initialFocusId={engineShown.value}
      items={engineShown.options.map(option => ({id: option.value, label: option.label, description: option.description, selected: option.value === engineShown.value,
        onSelect: () => {
          const opening = engineRef.current;
          if (controller.getSnapshot().busy || !opening || opening !== engineShown || !engineAlive(opening)) return;
          const choice = opening.choices.find(choice => String(choice.value) === option.value);
          if (!choice) return;
          retireEngine();
          opening.apply(option.value);
        }}))}/>}
    {theme.isTV && <Dialog testID="settings-language-sheet" open={languages !== undefined} onOpenChange={open => {if (!open) setLanguages(undefined);}} title={languages?.title} description={languages?.help} placement="side" width={640} initialFocusRef={firstLanguage}>
      {languages && <LanguageList initialFocusRef={firstLanguage} testID={languages.testID} label={languages.title} value={languages.value} available={languages.available} limit={languages.limit} onChange={languages.apply}/>}
    </Dialog>}
  </>;
}

/** tv-more24: the frame's QR card, inline on the profile page. The public URL and its
 * displayed form come from the accounts handoff so no credential or code can enter it. */
function ManagementCard({url, text}: {url: string; text: NativeShellRuntime['text']}) {
  const theme = useTheme(), address = publicTVHandoffAddress(url), t = text.t;
  return <View testID="settings-management-handoff" style={{flexDirection: 'row', alignItems: 'center', gap: 36, paddingVertical: 26, paddingHorizontal: 30,
    borderRadius: 18, backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: `${theme.colors.line}B3`, marginBottom: 30}}>
    <View style={{width: 224, height: 224, padding: 12, borderRadius: 14, backgroundColor: theme.colors.white}}><QRCode value={address.url} size={200}/></View>
    <View style={{flex: 1}}>
      <Text style={[shellType(28, 700, 36.4), {color: theme.colors.text, marginBottom: 14}]}>{t('account.tvHandoffTitle')}</Text>
      <Text style={[shellType(22, 400, 33), {color: theme.colors.textSecondary}]}>{stateSentenceParts(t('settings.openOnWeb', {url: '{url}'}), {url: address.display}, ['url']).map((part, index) => <Text key={index}
        style={part.strong ? {color: theme.colors.text, fontWeight: '700'} : undefined}>{part.text}</Text>)}</Text>
    </View>
  </View>;
}

/** tv-more frame 22's key caps: a drawn key beside the action it performs. */
function KeyCap({label}: {label: string}) {
  const theme = useTheme();
  return <View style={{minWidth: 44, height: 36, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: `${theme.colors.text}4D`, alignItems: 'center', justifyContent: 'center'}}>
    <Text style={[shellType(17, 800, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text>
  </View>;
}
