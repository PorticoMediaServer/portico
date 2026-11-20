import {readOwnerAwayLimit} from '../../../../../../packages/client-core/src/owner-settings-policy';
import {readServerUpdates, updatePillShown} from '../../../../../../packages/client-core/src/server-admin/server-updates';
import {useTVVoiceSearch} from '../../../screens/settings-live/use-tv-voice-search';
import {tvGroupEdges, tvSettingsResultGroups} from '../../../screens/settings-live/tv-settings-focus';
import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, Keyboard, NativeModules, Platform, type ScrollView, type View} from 'react-native';
import {settingsStructure, settingsRowId, searchSettings, findSettingsPage, type SettingsPage} from '../../../../../../packages/client-core/src/presentation/settings-structure';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {useTheme} from '../../../kit/theme';
import {useCovered, useFocusRegistry} from '../../../kit/focus';
import type {SettingsRowTargeting} from '../../../kit/shell';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeSettingsController} from '../../../screens/settings-live/settings-controller';
import type {NativeDevicePreferences} from '../../../screens/settings-live/device-preference-consumer';
import {setNativeScreenChromeHidden} from '../../../shell/chrome';
import {useScreenBack, useScreenEntry} from '../../../screens/settings-live/Screen';
import {registerBack} from '../../../kit/navigation/back';
import type {NativeShellRoute} from '../../../shell/routes';
import {readSettingsBinding, settingsBinding, type SettingsBindingValue} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {createNativeRecordingsController} from '../../../screens/settings-live/recordings-controller';
import {nativeRouteCrypto} from '../../../platform/server-connections';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';

const noFocusSubscription = () => () => {};
const noFocusSnapshot = () => 0;

/** settings.html wins over the structure's group order. */
const serverPageOrder = ['general', 'dashboard', 'history', 'libraries', 'remote-sources', 'metadata', 'segments', 'live', 'people', 'signin', 'remote', 'integrations', 'streaming', 'storage', 'troubleshooting'];


/** A page Settings can open on: Profile & account, or a server page a route names. */
export type NativeSettingsInitialPage = 'profile' | NonNullable<Extract<NativeShellRoute, {kind: 'server-settings'}>['page']>;
export type SettingsProps = {ports: NativeSettingsLivePorts; controller: NativeSettingsController; runtime: NativeShellRuntime;
  devicePreferences: NativeDevicePreferences; dashboard?: NativeDashboardController; initialPage?: NativeSettingsInitialPage; initialChannelId?: string};

export function useSettingsPage({ports, controller, runtime, devicePreferences, dashboard, initialPage}: SettingsProps) {
  const theme = useTheme(), t = runtime.text.t, registry = useFocusRegistry();
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
  const compact = theme.platform === 'phone';
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
  // The Streaming document supplies the owner's away limit. A refused optional read
  // leaves the status out without asking for the management PIN (#595).
  const [awayLimit, setAwayLimit] = useState<SettingsBindingValue>();
  useEffect(() => {
    if (!managesServer) return;
    let alive = true;
    void controller.lifetime.request(next => readOwnerAwayLimit(ports.context.api, next))
      .then(({value: awayLimit}) => {
        const binding = settingsBinding('quality.awayLimit');
        const value = binding ? readSettingsBinding(binding, {forms: {awayLimit}}) : undefined;
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
  // J-32: an account page (Profile & account) opens for every profile; a server page only for one that may see it.
  // A route's page opens only when this viewer has it (a server page needs its own admission).
  const opensInitial = !theme.isTV && !!initialPage && [...accountPages, ...serverPages].some(value => value.id === initialPage);
  const [selected, setSelected] = useState(() => opensInitial ? initialPage : !theme.isTV && !compact ? tabletDefaultPage : accountPages[0]?.id), [query, setQuery] = useState(''), [pushed, setPushed] = useState(!theme.isTV && (opensInitial || !compact));
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
  const [searching, setSearching] = useState(false), [searchRevision, setSearchRevision] = useState(0);
  const trackSearchFocus = theme.isTV && searching;
  useSyncExternalStore(trackSearchFocus ? registry.subscribe : noFocusSubscription, trackSearchFocus ? registry.getSnapshot : noFocusSnapshot, noFocusSnapshot);
  const voiceSearch = useTVVoiceSearch(theme.isTV && searching, controller.lifetime, setQuery, runtime.reportError);
  const [restoreRevision, setRestoreRevision] = useState(0);
  const [target, setTarget] = useState<Readonly<{opening: object; page: string; rowId: string; serial: number}>>();
  const targetSerial = useRef(0), aimedCovered = useRef<number | undefined>(undefined), covered = useCovered();
  const pageScroll = useRef<ScrollView>(null), pageViewport = useRef<Readonly<{scroll: ScrollView | null; height: number}> | undefined>(undefined);
  const liveControllers = useMemo(() => {
    if (!managesServer || theme.isTV) return undefined;
    const lifetime = controller.lifetime;
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => ports.context.api.request<T>(path, method, body, next), signal)};
    // A source's pause switch carries its revision in a header: the app's header-sending call, guarded by this screen.
    return {owner: new NativeLiveOwnerController(lifetime, withGuardedRaw(api, ports.context.api, () => lifetime.assert()), () => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('')), recordings: createNativeRecordingsController(lifetime, api, ports.dvrRequestId)};
  }, [controller, ports, managesServer, theme.isTV]);
  useEffect(() => () => {liveControllers?.owner.dispose(); liveControllers?.recordings.dispose();}, [liveControllers]);
  // N5-5: phone and tablet search every page this viewer has, Server pages included; TV's corpus stays the account pages (serverPages is empty there).
  const searchable = useMemo(() => ({headings: structure.headings.flatMap(heading => heading.id === 'account' ? [heading]
    : heading.id === 'server' && serverPages.length ? [{...heading, pages: serverPages}] : [])}), [structure, serverPages]);
  const matches = searchSettings(searchable, query, t);
  /** A result's address names its heading and page: open that page (a Server page by its own id), aimed at the result's row.
   * Touch (settings.html): the row scrolls to the middle and flashes once. TV (tv-more 34): search closes and the page opens
   * with the row highlighted and focused. */
  const openMatch = (address: string, rowId?: string) => {
    const found = findSettingsPage(searchable, address);
    if (!found || !viewCurrent()) return;
    if (theme.isTV) {setSearching(false); setQuery('');}
    else Keyboard.dismiss();
    choose(found.page.id);
    if (rowId) setTarget({opening, page: found.page.id, rowId, serial: ++targetSerial.current});
  };
  const matchingPages = accountPages.flatMap(value => {
    const found = matches.filter(match => match.address === value.id);
    if (!found.length) return [];
    return [{...value, sections: value.sections.map(section => ({...section, advanced: false, rows: section.rows.filter(row => found.some(match => !match.rowId || match.rowId === settingsRowId(row)))})).filter(section => section.rows.length)}];
  });
  const serverPage = serverPages.find(value => value.id === selected);
  const page = serverPage ?? accountPages.find(value => value.id === selected) ?? accountPages[0];
  // The opened row belongs to the opening and page a match opened; another page (TV column focus included) or a
  // replacement opening drops it.
  const aimedHere = !!target && target.opening === opening && target.page === page?.id;
  if (target && !aimedHere) setTarget(undefined);
  const aimed = aimedHere ? target : undefined;
  // The aim as committed: a measurement answers only for the aim, opening and scroll host it was asked for. Each opening
  // (serial) is revealed once. Fabric commits the row's layout effects before this page's, so a row that asks before its
  // aim and scroll host are committed waits here and is revealed at this page's commit; it answers false, so its next
  // native layout asks again if the scroll host is still missing. It also waits for this page's scroller to report a real
  // height (on a first opening the scroller has only just mounted, and Fabric's measureLayout answers at once), and that
  // layout reveals it.
  const committedAim = useRef<typeof target>(undefined), revealedSerial = useRef<number | undefined>(undefined);
  const waitingRow = useRef<Readonly<{row: View; serial: number}> | undefined>(undefined);
  const reveal = useCallback((row: View, serial: number) => {
    if (revealedSerial.current === serial) return true;
    const aim = committedAim.current, scroll = pageScroll.current, host = scroll?.getNativeScrollRef?.(), viewport = pageViewport.current;
    if (aim?.serial !== serial || !scroll || !host || viewport?.scroll !== scroll || !(viewport.height > 0)) {waitingRow.current = {row, serial}; return false;}
    revealedSerial.current = serial; waitingRow.current = undefined;
    row.measureLayout(host, (_x, y, _width, height) => {
      if (committedAim.current !== aim || aim.opening !== openingRef.current || pageScroll.current !== scroll) return;
      scroll.scrollTo({y: Math.max(0, y - (viewport.height - height) / 2), animated: false});
    });
    return true;
  }, []);
  const revealWaiting = useCallback(() => {
    const waiting = waitingRow.current, aim = committedAim.current;
    if (waiting && waiting.serial === aim?.serial) reveal(waiting.row, waiting.serial); else waitingRow.current = undefined;
  }, [reveal]);
  useLayoutEffect(() => {committedAim.current = aimed; revealWaiting();}, [aimed, revealWaiting]);
  const pageScrollLayout = useCallback((height: number) => {pageViewport.current = {scroll: pageScroll.current, height}; revealWaiting();}, [revealWaiting]);
  const rowTargeting = useMemo<SettingsRowTargeting>(() => !aimed ? {} : theme.isTV ? {highlight: aimed.rowId}
    : {flash: {rowId: aimed.rowId, serial: aimed.serial, reveal}}, [aimed, theme.isTV, reveal]);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const column = ids.surface('settings.pages'), rows = ids.surface(`settings.rows.${page?.id ?? ''}`), keyboard = ids.surface('settings.keyboard'), searchField = ids.surface('settings.search');
  // Actual attached controls decide which result groups participate in the focus graph.
  // Conditional/unavailable rows must not strand focus in an empty intermediate group.
  const matchRegions = tvSettingsResultGroups(matchingPages.map(value => ids.surface(`settings.matches.${value.id}`)), registry.entryAddress);
  const matchEdges = (id: string) => tvGroupEdges(matchRegions, id, searchField);
  const enter = useScreenEntry(theme.isTV && page ? {regionId: column, childId: accountPages[0].id} : undefined, controller.lifetime.current);
  useDestinationEntryFocus({visible: theme.isTV && !searching, intent: restoreRevision ? 'enter' : 'restore', revision: restoreRevision}, page ? {regionId: column, childId: page.id} : undefined);
  // TV: the page a match opened enters on that row. Asked once per opening and only while Settings is the top layer: an
  // opening that was covered before its row took focus never asks, then or after it is uncovered.
  if (covered && aimed) aimedCovered.current = aimed.serial;
  useDestinationEntryFocus({visible: theme.isTV && !searching && !covered && !!aimed && aimedCovered.current !== aimed.serial, intent: 'enter', revision: aimed?.serial ?? 0},
    aimed ? {regionId: rows, childId: aimed.rowId} : undefined);
  const remoteSourcesPage = !theme.isTV && page?.id === 'remote-sources';
  const remoteBack = useRef<(() => boolean) | undefined>(undefined);
  const setRemoteBack = useCallback((handler: (() => boolean) | undefined) => {remoteBack.current = handler;}, []);
  const back = useCallback(() => {
    if (!viewCurrent()) return false;
    if (remoteSourcesPage) {
      if (remoteBack.current?.()) return true;
      pageState.current = {...pageState.current, pushed: false}; setPushed(false);
      if (theme.platform !== 'phone' && accountPages[0]) {pageState.current = {...pageState.current, selected: accountPages[0].id}; setSelected(accountPages[0].id);}
      return true;
    }
    if (searching) {setSearching(false); setQuery(''); setRestoreRevision(value => value + 1); return true;}
    // W-14: Back from anywhere in the page's content (its rows, or a custom page's own panels)
    // returns to that page in the column, never out of Settings.
    const focused = registry.capture()?.regionId;
    if (theme.isTV && page && focused && focused !== column && focused !== searchField && focused !== ids.rail && !focused.startsWith(keyboard)) return registry.focus({regionId: column, childId: page.id});
    if (registry.capture()?.regionId === rows && page) return registry.focus({regionId: column, childId: page.id});
    if (page && page.id !== accountPages[0]?.id) {pageState.current = {...pageState.current, selected: accountPages[0].id}; setSelected(accountPages[0].id); registry.focus({regionId: column, childId: accountPages[0].id}); return true;}
    return false;
  }, [registry, column, rows, page, accountPages, searching, remoteSourcesPage, theme.platform, theme.isTV, viewCurrent, searchField, keyboard, ids]);
  const account = ports.context.account.account;
  const signedIn = account.hostedAccountId
    ? t('settings.signedInWithAccount', {server: ports.serverName()})
    : t('settings.signedInAs', {server: ports.serverName(), username: account.username});
  // The drawn sheet heads the profile, then the routes this build can really open.
  const unreadNotifications = shell.unreadNotificationCount ?? 0;
  // owner-dashboard 1b/1c: on a phone the Dashboard is reached from a card above the shortcuts, not a Server row.
  const dashboardCard = compact && !theme.isTV && serverPages.some(value => value.id === 'dashboard');
  const serverGroupPages = dashboardCard ? serverPages.filter(value => value.id !== 'dashboard') : serverPages;
  // The card's Update pill: whether the server says a newer build is out, read once per showing. Any failure,
  // including an answer this app cannot read, just leaves the pill off; the catch comes after the read so nothing
  // thrown while reading the answer goes unhandled.
  const [updatePending, setUpdatePending] = useState(false);
  useEffect(() => {
    if (!dashboardCard || !managesServer) {setUpdatePending(false); return;}
    let live = true;
    void controller.lifetime.request(next => readServerUpdates(ports.context.api, next))
      .then(updates => {if (live && controller.lifetime.current()) setUpdatePending(updatePillShown(updates));})
      .catch(() => {});
    return () => {live = false;};
  }, [controller, dashboardCard, managesServer, ports]);
  const sheetShortcuts = shell.account.actions.filter(action => action.id !== 'settings').map(action => ({...action,
    onPress: () => {if (viewCurrent()) action.onPress();},
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
  useScreenBack(runtime, back, theme.isTV, {kind: 'settings'});
  // NT-4 / TVF-A4: a tablet draws Settings as a page, not a sheet, so no overlay owns Back there. The same
  // ladder runs as this page's Back step: a Remote sources wizard step, then search, then the page; then the shell.
  useLayoutEffect(() => !theme.isTV && !compact ? registerBack(back, {scope: 'page', active: viewCurrent}) : undefined, [back, compact, theme.isTV, viewCurrent]);
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
  const choose = (id: string) => {if (!viewCurrent()) return; pageState.current = {...pageState.current, selected: id, pushed: !theme.isTV}; setSelected(id); setTarget(undefined); if (!theme.isTV) setPushed(true);};
  const phoneBack = () => {if (!viewCurrent()) return; if (!remoteSourcesPage || !remoteBack.current?.()) popPage();};
  const viewerRole = ports.context.account.account.role === 'owner' ? 'owner' as const : ports.context.account.canManage ? 'administrator' as const : 'member' as const;
  const customPage = dashboardPage || librariesPage || livePage || troubleshootingPage || storagePage || integrationsPage || generalPage || streamingPage || segmentsPage || peoplePage || signInPage || metadataPage || remoteSourcesPage || remoteAccessPage;
  const accountGroups = compact ? [orderedPages] : grouped(orderedPages, theme.isTV ? tvPageGroups : tabletAccountClusters);
  const serverGroups = grouped(serverGroupPages, compact ? [] : tabletServerClusters);
  const focusSearch = () => {if (theme.isTV && !searching) {setSearching(true); setSearchRevision(value => value + 1);}};
  const openDashboard = () => {if (viewCurrent()) navigation.open({kind: 'server-dashboard'}, ports.context.authority());};
  const openServerPage = (id: string) => {
    if (id === 'dashboard' && (dashboard === undefined || compact)) openDashboard(); else choose(id);
  };
  const sheetChanged = (open: boolean, reason: string) => {
    if (open || !viewCurrent() || closingRef.current) return;
    if (pushed && (reason === 'back' || reason === 'escape')) phoneBack();
    else closeSettings();
  };
  return {theme, t, shell, state, ids, page, compact, pushed, query, setQuery, searchRevision, searching, searchField, keyboard, rows, column,
    selectPage: setSelected, choose, focusSearch, sheetShortcuts, unreadNotifications, accountGroups, serverGroups, orderedPages, enter, openMatch,
    matches, matchingPages, matchRegions, matchEdges, voiceSearch, sheetChanged, closing, closeSettings, finishClose, phoneBack, signedIn,
    serverPage, viewerRole, dashboardCard, dashboardDenied, openDashboard, openServerPage, updatePending, leaveServerPage, setRemoteBack,
    librariesPage, livePage, troubleshootingPage, storagePage, integrationsPage, generalPage, streamingPage, segmentsPage, peoplePage,
    signInPage, metadataPage, historyPage, remoteSourcesPage, remoteAccessPage, dashboardPage, customPage, liveControllers,
    pageScroll, pageScrollLayout, rowTargeting, aimed, reportedOutputs, awayLimitLabel};
}
