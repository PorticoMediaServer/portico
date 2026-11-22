import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type { View,VirtualizedList } from 'react-native';
import {Platform, useWindowDimensions} from 'react-native';
import {useAnimatedStyle, useDerivedValue} from 'react-native-reanimated';
import {dayPrimeTime} from '../../../../../../packages/client-core/src/channel-guide';
import type { RecordingDraft } from '../../../../../../packages/client-core/src/dvr';
import {defaultRecordingOptions} from '../../../../../../packages/client-core/src/dvr';
import {floorTo, guideDayMenu, guideRange, layoutRow, legacyChannel, legacyChannelRow, legacyProgram, legacyProgramme, localDayStart, pxPerMsFor, SLOT_MS, type GuideCell, type GuideChannelRow, type GuideProgram} from '../../../../../../packages/client-core/src/guide';
import {GuideFeatureClient, type GuideAiring, type GuideReminder} from '../../../../../../packages/client-core/src/guide/directory';
import {liveGuideStale, liveServerWideNotice} from '../../../../../../packages/client-core/src/presentation/live-availability';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {useDockClearance} from '../../../kit/dock/DockClearance';
import {useFocusRegistry} from '../../../kit/focus';
import type { FocusAddress } from '../../../kit/focus/registry';
import {guideSpec} from '../../../kit/live/Guide';
import {registerBack} from '../../../kit/navigation/back';
import {useRailHold} from '../../../kit/navigation/rail-hold';
import {type ToastMessage} from '../../../kit/overlays';
import {usePullRefresh} from '../../../kit/states/usePullRefresh';
import {useTheme} from '../../../kit/theme';
import {useScreenReader} from '../../../kit/useScreenReader';
import {usePlatformPosture} from '../../../platform/platformPosture';
import {useTVRemoteActivity} from '../../../platform/tv-keys';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVPageBack} from '../../../screens/destinations/useTVPageBack';
import {nativeGuidePanRange, nativeGuidePanTarget} from '../../../screens/settings-live/guide-pan-model';
import {cellFocusId, nativeGuideClock, nativeGuideDate, nativeGuideRegion, nativeLibraryChannelEditorRoute, type GuideCellIdentity} from '../../../screens/settings-live/guide-presentation';
import type { NativeLiveController } from '../../../screens/settings-live/live-controller';
import type { NativeLiveOwnerController } from '../../../screens/settings-live/live-owner-controller';
import type { NativeSettingsLivePorts } from '../../../screens/settings-live/ports';
import {seriesFormDefaults, type SeriesForm} from '../../../screens/settings-live/recording-rule-presentation';
import type { NativeRecordingsController } from '../../../screens/settings-live/recordings-controller';
import {useScreenBack} from '../../../kit/layout/Screen';
import {nativeServerAdmission} from '../../../screens/settings-live/server/tv-server-reader';
import {GUIDE_RECOVERY_SETTLE_MS, guideReentryNext, type GuideReentry, type GuideReentryEvent} from '../../../screens/settings-live/tv-settings-focus';
import {useGuideTimePan} from '../../../screens/settings-live/useGuideTimePan';
import {sameAuthority, shellLayout} from '../../../shell/model';
import type { NativeShellRuntime } from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {publicTVHandoffAddress} from '../../../tv/auth/TVHandoff';
import {tvGuideFocusStart} from '../../../tv/layout/guide-focus';

export type GuidePageProps = {controller: NativeLiveController; recordings: NativeRecordingsController; owner: NativeLiveOwnerController; ports: Pick<NativeSettingsLivePorts, 'context' | 'dvrRequestId' | 'requestId'>; runtime: NativeShellRuntime};

export function useLiveGuide({controller, recordings, owner, ports, runtime}: GuidePageProps) {
  const theme = useTheme(), t = runtime.text.t, dimensions = useWindowDimensions();
  const dockTop = useDockClearance();
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), guide = controller.guide;
  const data = useSyncExternalStore(guide.subscribe, guide.getSnapshot);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const compact = !theme.isTV && theme.width < 600, screenReader = useScreenReader();
  const phone = compact;
  const tabletMedium = !theme.isTV && theme.platform === 'tablet', phoneChrome = phone || tabletMedium;
  // The authored phone header stack covers the medium tablet too; wide layouts keep the desktop controls row.
  const expandedTouch = !theme.isTV && !phoneChrome;
  // foldable7: only a reported vertical crease for this exact window may split the page;
  // an absent posture report keeps the single-page guide and its panel.
  const posture = usePlatformPosture();
  const crease = posture.status === 'available' && posture.window?.width === dimensions.width && posture.window.height === dimensions.height
    ? posture.folds.find(value => value.orientation === 'vertical' && value.bounds.left > 0 && value.bounds.left < value.bounds.right && value.bounds.right <= dimensions.width) : undefined;
  const insetLeft = compact || theme.isTV ? 0 : 20;
  const insetRight = compact || theme.isTV ? 0 : 20;
  const [now, setNow] = useState(serverNow), [start, setStart] = useState(() => floorTo(serverNow(), SLOT_MS) - (theme.isTV || phoneChrome ? SLOT_MS : 0)), [measuredWidth, setMeasuredWidth] = useState(theme.isTV ? 1660 : theme.width - 40);
  // phone-guide 2: until the user chooses, a phone opens on the list (phones are upright only), and a remembered
  // view always wins.
  const view = state.view ?? (shellLayout(theme.width, dimensions.height, theme.isTV).kind === 'phone' ? 'list' : 'guide');
  const setView = (value: 'guide' | 'list') => {if (value === 'list') goNow(); controller.chooseView(value);};
  const [selected, setSelected] = useState<Readonly<{channel: GuideChannelRow; program?: GuideProgram}>>();
  const selectedRef = useRef(selected); selectedRef.current = selected;
  const [channelMenu, setChannelMenu] = useState<Readonly<{channel: GuideChannelRow; scope: string}>>(), [channelMenuBusy, setChannelMenuBusy] = useState(false), [channelMenuError, setChannelMenuError] = useState<unknown>();
  const channelMenuRef = useRef(channelMenu); channelMenuRef.current = channelMenu;
  const channelMenuAnchor = useRef<View | null>(null);
  // tablet-play6/7: a chosen programme docks in the right pane on tablets as well as
  // across a real fold; with nothing selected the guide takes the whole width. The page
  // measures its own content box because the shell rail or sidebar sits outside it, so
  // window-space crease bounds are shifted by that edge before anything is laid out here.
  const content = useRef<View>(null);
  const [contentViewport, setContentViewport] = useState<Readonly<{left: number; width: number}>>();
  const measureContent = () => {
    content.current?.measureInWindow((left, _top, measured) => {
      if (!(measured > 0)) return;
      setContentViewport(current => current && Math.abs(current.left - left) < .5 && Math.abs(current.width - measured) < .5 ? current : {left, width: measured});
    });
  };
  const contentWidth = contentViewport?.width ?? 0, contentLeft = contentViewport?.left ?? 0;
  const creaseLeft = crease ? Math.max(0, crease.bounds.left - contentLeft) : undefined;
  const creaseRight = crease ? Math.max(0, crease.bounds.right - contentLeft) : undefined;
  const [recording, setRecording] = useState(false), [recordError, setRecordError] = useState<unknown>();
  const [series, setSeries] = useState<Readonly<{draft: RecordingDraft; defaults?: SeriesForm}>>(), [toast, setToast] = useState<ToastMessage | null>(null);
  const [reminders, setReminders] = useState<ReadonlyMap<string, GuideReminder>>(new Map()), [later, setLater] = useState<readonly GuideAiring[]>([]);
  const [laterRecordings, setLaterRecordings] = useState<ReadonlyMap<string, Readonly<{id: string; state: string}>>>(new Map()), [laterRecording, setLaterRecording] = useState<ReadonlySet<string>>(new Set());
  const [recordingsOpen, setRecordingsOpen] = useState(false), [recordingsReturn, setRecordingsReturn] = useState(0), [ownerOpen, setOwnerOpen] = useState(false);
  const [managementReturn, setManagementReturn] = useState(0);
  // Show all's one-shot entry ask (TV): armed at the press, bound to the revision it produces, and disarmed by anything
  // the remote or Back does, leaving Live or one of its pages, a guide cell taking focus, a filter chosen again or any
  // later revision (guideReentryNext). `reentryBaseline` is where native recovery left focus once it settled
  // (GUIDE_RECOVERY_SETTLE_MS after the target revision): focus anywhere else at fire time is where the person put it (guideFocusMoved).
  const [guideReentry, setGuideReentry] = useState<GuideReentry>(), guideReentryRef = useRef<GuideReentry | undefined>(undefined);
  const tvRegistry = useFocusRegistry(), reentryBaseline = useRef<Readonly<{at?: FocusAddress}> | undefined>(undefined);
  const reentry = (event: GuideReentryEvent) => {
    const next = guideReentryNext(guideReentryRef.current, event);
    if (event.kind === 'press' || next === undefined) reentryBaseline.current = undefined;
    if (next !== guideReentryRef.current) {guideReentryRef.current = next; setGuideReentry(next);}
  };
  const [management, setManagement] = useState<Readonly<{opening: object; url: string}>>();
  const navigation = ports.context.navigation;
  const route = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const shell = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
  useTVRemoteActivity(guideReentry !== undefined, raw => reentry({kind: 'remote', eventType: raw.eventType}));
  useLayoutEffect(() => {
    reentry({kind: 'revision', revision: state.revision});
    const armed = guideReentryRef.current;
    if (armed?.target !== state.revision || reentryBaseline.current) return;
    const settle = setTimeout(() => {if (guideReentryRef.current === armed && !reentryBaseline.current) reentryBaseline.current = {at: tvRegistry.attachedFocus()};}, GUIDE_RECOVERY_SETTLE_MS);
    return () => clearTimeout(settle);
  }, [state.revision, tvRegistry]);
  const onLive = shell.destination.kind === 'live';
  useLayoutEffect(() => {if (!onLive || recordingsOpen || ownerOpen || management || selected) reentry({kind: 'leave'});}, [onLive, recordingsOpen, ownerOpen, management, selected]);
  // An opening cannot be revived by returning to this retained Guide or by replacing
  // the viewer. Retire in render before an old handler or QR can reach its successor.
  const scopeKey = JSON.stringify([route.revision, route.destination, shell.authority, ports.context.account.account.role,
    ports.context.account.account.primaryProfileId, ports.context.account.canManage, ports.context.account.account.disabled,
    theme.isTV, shell.contentVisible, shell.mediaExpanded, shell.homeObscured]);
  const channelMenuScope = useRef(scopeKey); channelMenuScope.current = scopeKey;
  const managementOwner = useMemo(() => ({retired: false, binding: {controller, runtime, context: ports.context, scopeKey}}), [controller, runtime, ports.context, scopeKey]);
  const managementOwnerRef = useRef(managementOwner);
  if (managementOwnerRef.current !== managementOwner) managementOwnerRef.current.retired = true;
  managementOwnerRef.current = managementOwner;
  useLayoutEffect(() => {managementOwner.retired = false; return () => {managementOwner.retired = true;};}, [managementOwner]);
  const managementCurrent = () => {
    const owns = () => !managementOwner.retired && managementOwnerRef.current === managementOwner && !runtime.retirementSignal?.aborted;
    // Runtime/navigation getters are guarded after disposal. Refuse the original
    // opening first, including retirement caused synchronously by a caller predicate.
    if (!owns() || !controller.lifetime.current() || !owns() || !ports.context.current() || !owns()
      || !navigation.isCurrent() || !owns()) return false;
    const current = runtime.getSnapshot();
    if (!owns()) return false;
    const actualRoute = navigation.getSnapshot();
    if (!owns()) return false;
    const authority = ports.context.authority();
    return owns() && sameAuthority(controller.lifetime.authority, authority)
      && sameAuthority(controller.lifetime.authority, current.authority)
      && actualRoute.revision === route.revision && current.destination.kind === 'live' && actualRoute.destination.kind === 'live'
      && current.contentVisible !== false && !current.mediaExpanded && !current.homeObscured
      && nativeServerAdmission(ports.context.account, controller.lifetime) === 'owner' && owns();
  };
  const managementActive = management?.opening === managementOwner && managementCurrent();
  const managementRef = useRef(management); managementRef.current = management;
  const managementOwnsPage = () => managementRef.current?.opening === managementOwner && managementCurrent();
  const closeManagement = () => {
    if (!management || managementRef.current !== management || !managementCurrent()) return false;
    setManagement(undefined); setManagementReturn(value => value + 1); return true;
  };
  useScreenBack(runtime, closeManagement, theme.isTV && managementActive, {kind: 'live'});
  const [initialOwnerAction, setInitialOwnerAction] = useState<'add' | 'discover'>();
  const opener = useRef<View | null>(null), selectedOpener = useRef<View | null>(null);
  const list = useRef<VirtualizedList<number>>(null), visible = useRef({first: 0, last: 12});
  const guideOffset = useRef(0);
  // motion.html 2.13: the guide refetches in place (old programmes stay) while the sources re-read.
  const pull = usePullRefresh(() => {guide.invalidate(); return controller.loadSources();}, runtime.reportError);
  const halfHour = theme.isTV ? 240 : phoneChrome ? 90 : theme.width < 1024 ? 90 : 120;
  const channelWidth = theme.isTV ? 320 : tabletMedium ? 60 : compact ? 60 : 192;
  // tablet-play6/7: 400 pt beside the expanded guide, 340 pt at medium width, and only when
  // the room left over still gives the guide a usable lane. A reported hinge band belongs
  // to neither side: the guide ends at its left edge and the pane starts after its right.
  const panelWidth = Math.max(0, Math.min(theme.width >= 1024 ? 400 : 340, contentWidth - channelWidth - 100));
  const paneLeft = creaseRight ?? Math.max(0, contentWidth - panelWidth), guideEnd = creaseLeft ?? paneLeft;
  const twoPane = !!selected && !theme.isTV && !phone && view === 'guide' && contentWidth > 0
    && guideEnd >= channelWidth + 100 && contentWidth - paneLeft >= 300;
  // tablet-play6: the sidebar folds to the rail while the programme panel is open beside the guide, and only while the
  // guide is the page showing: any page pushed over Live (a title, a channel editor, Recordings, the owner's pages) or the
  // expanded player releases it, and it folds again when the guide is back with the panel still open.
  const liveOnTop = route.route?.kind === 'shell' && route.route.destination.kind === 'live' && shell.contentVisible !== false && !shell.mediaExpanded;
  useRailHold(!theme.isTV && twoPane && onLive && liveOnTop && !ownerOpen && !recordingsOpen && !management);
  const width = Math.max(0, measuredWidth - insetLeft - (twoPane ? Math.max(0, contentWidth - guideEnd) : insetRight));
  const rowHeight = theme.isTV ? 92 : view === 'list' ? 100 : 72, rowGap = theme.isTV ? 8 : phoneChrome ? 0 : 6, px = pxPerMsFor(halfHour), end = start + Math.max(halfHour, width - channelWidth) / px;
  useEffect(() => {
    void controller.loadSources();
    const clock = repeat(async () => {setNow(serverNow());}, {everyMs: 30_000, backoff: {baseMs: 30_000, maxMs: 30_000, jitter: 'none'}, immediate: false});
    return () => clock.stop();
  }, [controller]);
  const features = useMemo(() => new GuideFeatureClient(controller.api, controller.lifetime.authority.serverId), [controller]);
  useEffect(() => () => features.dispose(), [features]);
  useEffect(() => {
    if (!twoPane || Platform.OS !== 'android') return;
    const subscription = registerBack(() => {if (!selectedRef.current) return false; setSelected(undefined); return true;}, {scope: 'page', active: () => {const now = runtime.navigation?.getSnapshot().route; return now?.kind === 'shell' && now.destination.kind === 'live';}});
    return () => subscription();
  }, [runtime.navigation, twoPane]);
  // Later on reads this channel after the selected programme, separately from its repeat airings.
  useEffect(() => {
    if (!selected) {setLater([]); setLaterRecordings(new Map()); return;}
    let live = true;
    setLater([]); setLaterRecordings(new Map());
    const programmeId = selected.program?.id;
    if (programmeId) void features.programme(programmeId).then(async detail => {
      if (!live) return;
      const current = {channel: detail.channel, programme: detail.programme};
      setSelected(value => value?.program?.id === programmeId ? {...value, channel: legacyChannelRow(detail.channel), program: legacyProgram(detail.programme)} : value);
      const airings = await features.later(detail.channel, detail.programme);
      if (!live) return;
      setLater(airings);
      const reminders = await features.remindersFor([current, ...airings]);
      if (live) setReminders(new Map(reminders.items.map(item => [item.airing.programme.id, item])));
    }).catch(error => {if (live && controller.lifetime.current()) runtime.reportError(error);});
    return () => {live = false;};
  }, [selected?.program?.id, features]); // eslint-disable-line react-hooks/exhaustive-deps
  const remind = (airing: GuideAiring, existing: GuideReminder | undefined) => {
    const work = existing ? features.dismissProgramme(existing).then(() => undefined) : features.remindProgramme(airing);
    void work.then(value => {
      if (!controller.lifetime.current()) return;
      if (!existing && value) setReminders(current => new Map(current).set(value.airing.programme.id, value));
      if (existing) setReminders(current => {const next = new Map(current); next.delete(airing.programme.id); return next;});
    }).catch(error => {if (controller.lifetime.current()) runtime.reportError(error);});
  };
  // A later airing is scheduled exactly as the selected programme is: same call, same options.
  const recordLater = (airing: GuideAiring) => {
    const channel = selected && legacyChannel(selected.channel), id = airing.programme.id;
    if (!channel || laterRecording.has(id)) return;
    setLaterRecording(current => new Set(current).add(id));
    void recordings.schedule({channel, programme: airing.programme, series: false}, defaultRecordingOptions).then(result => {
      if (!controller.lifetime.current() || !result) {if (controller.lifetime.current()) setRecordError(recordings.getSnapshot().error); return;}
      setLaterRecordings(current => new Map(current).set(id, {id: result.value.id, state: result.value.state}));
      if (!theme.isTV) void recordings.loadSummary();
      guide.invalidate();
    }, error => {if (controller.lifetime.current()) setRecordError(error);}).finally(() => {
      if (controller.lifetime.current()) setLaterRecording(current => {const next = new Set(current); next.delete(id); return next;});
    });
  };
  const askSeries = async (channel: GuideChannelRow, programme: GuideProgram) => {
    const rawChannel = legacyChannel(channel), rawProgramme = legacyProgramme(programme);
    if (!rawChannel || !rawProgramme) return;
    let defaults: SeriesForm | undefined;
    try {defaults = seriesFormDefaults(await owner.dvrSettings());} catch {defaults = undefined;}
    if (!controller.lifetime.current()) return;
    setSeries({draft: {channel: rawChannel, programme: rawProgramme, series: true}, defaults});
  };
  useEffect(() => {controller.setViewport({firstRow: visible.current.first, lastRow: visible.current.last, start, end});}, [controller, guide, start, end]);
  const source = state.sources.find(value => (value.provenance === 'library-channel' ? 'library' : value.id) === state.options.sourceId);
  const selectedSources = useMemo(() => source ? [source] : state.sources, [source, state.sources]);
  const tvRange = nativeGuidePanRange(selectedSources, end - start);
  const revealTVCell = (index: number, cell?: GuideCell) => {
    reentry({kind: 'focus'});
    const shell = runtime.getSnapshot();
    if (!theme.isTV || managementOwnsPage() || !controller.lifetime.current() || shell.destination.kind !== 'live' || shell.contentVisible === false || shell.mediaExpanded) return;
    // Native focus has already arrived. This only brings that native host's row/time into view.
    if (index < visible.current.first || index >= visible.current.last) list.current?.scrollToIndex({index, animated: false, viewPosition: .5});
    let focusStart = start;
    if (cell) {
      const anchor = tvRegistry.cellPosition(grid)?.time ?? cell.start;
      const target = tvGuideFocusStart(anchor, start, end, SLOT_MS);
      const next = nativeGuidePanTarget(target, end - start, tvRange);
      if (next !== undefined && next !== start) {focusStart = next; setStart(next);}
    }
    controller.setViewport({firstRow: Math.min(index, visible.current.first), lastRow: Math.max(index + 1, visible.current.last), start: focusStart, end: focusStart + (end - start)});
  };
  const pan = useGuideTimePan({enabled: !theme.isTV && view === 'guide' && !recordingsOpen && (!selected || twoPane) && channelMenu?.scope !== scopeKey, revision: state.revision, start, end, pxPerMs: px, channelWidth, sources: selectedSources, commit: setStart, current: controller.lifetime.current});
  const panPosition = pan.position, panActive = pan.active;
  const rulerShift = useDerivedValue(() => panActive ? start * px - panPosition.value : 0, [panActive, start, px]);
  const nowShift = useAnimatedStyle(() => {
    const from = panActive ? panPosition.value / px : start;
    return {transform: [{translateX: panActive ? start * px - panPosition.value : 0}], opacity: now >= from && now < from + (end - start) ? 1 : 0};
  }, [panActive, start, end, px, now]);
  const nowPillShift = useAnimatedStyle(() => {
    const marker = (now - (panActive ? panPosition.value / px : start)) * px;
    const pillWidth = guideSpec.nowPill / 2;
    return {left: Math.max(0, Math.min(marker - pillWidth / 2, width - channelWidth - pillWidth)) - marker};
  }, [panActive, start, px, now, width, channelWidth]);
  const pastShade = useAnimatedStyle(() => ({width: Math.max(0, Math.min((now - (panActive ? panPosition.value / px : start)) * px, width - channelWidth))}), [panActive, start, px, now, width, channelWidth]);
  const earlierNow = useAnimatedStyle(() => ({display: now < (panActive ? panPosition.value / px : start) ? 'flex' : 'none'}), [panActive, start, px, now]);
  const laterNow = useAnimatedStyle(() => ({display: now >= (panActive ? panPosition.value / px + (end - start) : end) ? 'flex' : 'none'}), [panActive, start, end, px, now]);
  const overscan = theme.isTV ? tvRange ? SLOT_MS : 0 : panActive ? (end - start) / 2 : 0;
  const groups = [...new Set((source ? [source] : state.sources).flatMap(value => value.groups))];
  const {locale, hour12} = nativeGuideRegion(runtime.text);
  const clock = useMemo(() => nativeGuideClock(locale, state.options.timezone, hour12, theme.isTV), [locale, state.options.timezone, hour12, theme.isTV]);
  const goNow = () => setStart(floorTo(now, SLOT_MS) - (theme.isTV || phoneChrome ? SLOT_MS : 0));
  const nowX = (now - start) * px, inNowSpan = now >= start && now < end;
  const range = guideRange(selectedSources), days = guideDayMenu(now, start, state.options.timezone, range);
  const daysCount = range.start !== undefined && range.end !== undefined ? days.length : 0;
  const today = localDayStart(now, state.options.timezone), currentDay = localDayStart(start, state.options.timezone);
  const open = (channel: GuideChannelRow, program?: GuideProgram, node?: View | null) => {selectedOpener.current = node ?? null; setRecordError(undefined); controller.clearMutationError(); setSelected({channel, program});};
  const openChannelMenu = (channel: GuideChannelRow, node?: View | null) => {
    if (!controller.lifetime.current()) return;
    channelMenuAnchor.current = node ?? null;
    controller.clearMutationError(); setChannelMenuError(undefined); setChannelMenuBusy(false);
    setChannelMenu({channel, scope: scopeKey});
  };
  // Owners change server visibility; Favorites remain the viewer's own preference.
  const changeChannel = (change: 'favorite' | 'hidden' | 'edit') => {
    const opening = channelMenu, channel = opening?.channel, raw = channel && legacyChannel(channel);
    const current = () => !!opening && channelMenuRef.current === opening && channelMenuScope.current === opening.scope
      && !runtime.retirementSignal?.aborted && controller.lifetime.current();
    if (!current() || !channel || !raw || channelMenuBusy) return;
    if (change === 'edit') {
      if (theme.isTV || raw.provenance !== 'library-channel' || !managementCurrent()) return;
      setChannelMenu(undefined);
      navigation.open(nativeLibraryChannelEditorRoute(raw.id), controller.lifetime.authority);
      return;
    }
    setChannelMenuBusy(true); setChannelMenuError(undefined);
    const proof = {signal: controller.lifetime.signal, isCurrent: current};
    if (change === 'hidden') {
      if (!managementCurrent() || channel.kind === 'library') {setChannelMenuBusy(false);return;}
      void owner.channelHidden(raw.sourceId, raw.id, !raw.serverHidden, proof).then(receipt => {
        if (!current()) return;
        controller.guide.reset();void controller.loadSources();setSelected(undefined);setChannelMenu(undefined);
        const undoScope = opening!.scope;
        const undoCurrent = () => channelMenuScope.current === undoScope && managementCurrent() && !runtime.retirementSignal?.aborted;
        setToast({id: `channel-${Date.now()}`, message: t(receipt.hidden ? 'channels.hiddenNotice' : 'channels.shownNotice'), actionOnTV: true,
          action: {label: t('action.undo'), onPress: () => {
            if (!undoCurrent()) return;
            void owner.channelHidden(raw.sourceId, raw.id, receipt.previousHidden, {signal: controller.lifetime.signal, isCurrent: undoCurrent}, receipt)
              .then(() => {if (undoCurrent()) {controller.guide.reset();void controller.loadSources();}}).catch(error => {if (undoCurrent()) runtime.reportError(error);});
          }}});
      }).catch(error => {if (current()) setChannelMenuError(error);}).finally(() => {if (channelMenuRef.current === opening) setChannelMenuBusy(false);});
      return;
    }
    const values = {favorite: !channel.favorite, hidden: raw.hidden};
    const work = controller.channelPreference(channel, values, ports.dvrRequestId, proof).then(committed => {
      if (!committed || !current()) return;
      setSelected(value => value?.channel.id === channel.id && value.channel.sourceId === channel.sourceId && value.channel.kind === channel.kind ? {...value, channel: committed} : value);
      setChannelMenu(undefined);
    });
    void work.catch(error => {if (current()) setChannelMenuError(error);}).finally(() => {if (channelMenuRef.current === opening) setChannelMenuBusy(false);});
  };
  const favorite = () => {
    const channel = selected?.channel, raw = channel && legacyChannel(channel);
    if (!channel || !raw) return;
    void controller.channelPreference(channel, {favorite: !channel.favorite, hidden: raw.hidden}, ports.dvrRequestId).then(committed => {
      if (committed && controller.lifetime.current()) setSelected(current => current?.channel.id === channel.id && current.channel.sourceId === channel.sourceId ? {...current, channel: committed} : current);
    });
  };
  const watch = (channel: GuideChannelRow) => {
    const raw = legacyChannel(channel);
    if (!raw || !raw.tuneAvailable) return;
    controller.lifetime.assert();
    ports.context.navigation.open({kind: 'channel', sourceId: raw.sourceId, channelId: raw.id,
      guide: Object.freeze({row: channel, options: Object.freeze({...state.options})})}, controller.lifetime.authority);
  };
  const select = (change: Parameters<NativeLiveController['select']>[0]) => {guideOffset.current = 0; visible.current = {first: 0, last: 12}; controller.select(change);};
  const chooseSource = (sourceId: string) => {
    const selectedSource = state.sources.find(value => (value.provenance === 'library-channel' ? 'library' : value.id) === sourceId);
    select({sourceId, kind: !selectedSource ? 'all' : selectedSource.provenance, group: undefined});
    list.current?.scrollToOffset({offset: 0, animated: false});
  };
  const sourceOptions = [{value: '', label: t('channels.all')}, ...state.sources.map(value => ({value: value.provenance === 'library-channel' ? 'library' : value.id, label: value.name}))];
  const chooseDay = (day: string) => Number(day) === today ? goNow() : setStart(dayPrimeTime(Number(day), state.options.timezone));
  const shortDate = useMemo(() => nativeGuideDate(locale, state.options.timezone, {weekday: 'short', month: 'short', day: 'numeric'}), [locale, state.options.timezone]);
  const longDay = useMemo(() => nativeGuideDate(locale, state.options.timezone, {weekday: 'long'}), [locale, state.options.timezone]);
  const fullDate = useMemo(() => nativeGuideDate(locale, state.options.timezone, {weekday: 'long', month: 'short', day: 'numeric'}), [locale, state.options.timezone]);
  const dayName = (day: number) => day === today ? t('guide.today') : day === days[1] ? t('guide.tomorrow') : longDay(day);
  // The drawn menu prints the weekday only where the day's own name is not already the weekday.
  const dayTrailing = (day: number) => day === today || day === days[1] ? shortDate(day) : nativeGuideDate(locale, state.options.timezone, {month: 'short', day: 'numeric'})(day);
  const dayNoteTime = nativeGuideClock(locale, state.options.timezone, hour12, false)(dayPrimeTime(today, state.options.timezone));
  const dayNote = days.length > 1 && dayNoteTime ? t('guide.dayMenuNote', {time: dayNoteTime, day: longDay(days[days.length - 1])}) : undefined;
  const sortOptions = [{value: 'number', label: t('channels.sortNumber')}, {value: 'name', label: t('channels.sortName')}];
  const chooseSort = (sort: string) => select({sort: sort as 'number' | 'name'});
  const controls = ids.surface('live.controls'), grid = ids.surface('live.grid'), filters = ids.surface('live.filters');
  useTVPageBack(runtime, {kind: 'live'}, theme.isTV && !managementActive, {
    registerTop: true, scope: JSON.stringify([state.revision, view]),
    current: () => controller.lifetime.current() && !recordingsOpen && !ownerOpen && !managementOwnsPage() && !selected,
    start: () => {
      const channel = guide.channelAt(0); if (!channel) return undefined;
      if (view === 'list') return {regionId: grid, childId: cellFocusId(channel, {kind: 'now'})};
      const openingStart = floorTo(now, SLOT_MS) - SLOT_MS;
      const offered = guide.programsFor(0, openingStart - overscan, openingStart + (end - start) + overscan);
      const programme = offered.programs.find(value => value.start <= now && value.end > now) ?? offered.programs[0];
      const cells = layoutRow(offered.programs, {viewStart: openingStart - overscan, viewEnd: openingStart + (end - start) + overscan,
        pxPerMs: px, now, gapPx: 6, tiers: {full: 300, title2: 150, title: 32}});
      const cell: GuideCellIdentity | undefined = programme ? {kind: 'programme', id: programme.id}
        : cells[0] ? {kind: 'gap', start: cells[0].start} : undefined;
      return cell ? {regionId: grid, childId: cellFocusId(channel, cell)} : undefined;
    },
    atTop: () => guideOffset.current <= 0 && start === floorTo(now, SLOT_MS) - SLOT_MS,
    scrollTop: () => {list.current?.scrollToOffset({offset: 0, animated: false}); goNow();},
  });
  // Counts are the server's own summaries (and the loaded guide's total while nothing is filtered);
  // a count the store does not know draws none.
  const filterActive = !!state.options.favorites || !!state.options.group, hasSources = state.sources.length > 0;
  // A filter chosen again before the guide took its entry cancels it: entry must never pull focus off a chip.
  useEffect(() => {if (filterActive) reentry({kind: 'filter'});}, [filterActive]);
  const groupCounts = new Map<string, number>();
  for (const value of selectedSources) for (const group of value.groupCounts) groupCounts.set(group.name, (groupCounts.get(group.name) ?? 0) + group.count);
  const favoriteCount = hasSources ? selectedSources.reduce((sum, value) => sum + value.favoriteCount, 0) : undefined;
  const allCount = filterActive ? hasSources ? selectedSources.reduce((sum, value) => sum + value.channelCount, 0) : undefined : data.total;
  const filterChips = [
    {id: 'all', testID: 'live-filter-all', label: t('channels.filterAll'), count: allCount, selected: !filterActive,
      onPress: () => select({favorites: false, group: undefined})},
    {id: 'favorites', testID: 'live-filter-favorites', label: t('channels.favorites'), count: favoriteCount, selected: state.options.favorites === true,
      onPress: () => select({favorites: !state.options.favorites, group: undefined})},
    ...(ports.context.account.account.role === 'owner' ? [{id: 'hidden', testID: 'live-filter-hidden', label: t('channels.showHidden'),
      selected: state.options.includeServerHidden === true, onPress: () => select({includeServerHidden: !state.options.includeServerHidden})}] : []),
    ...groups.map(group => ({id: `group:${group}`, testID: `live-filter-${group}`, label: group, count: groupCounts.get(group), selected: state.options.group === group,
      onPress: () => select({group, favorites: false})})),
  ];
  useDestinationEntryFocus({visible: theme.isTV && !managementActive && !recordingsOpen && recordingsReturn > 0, intent: 'restore', revision: recordingsReturn}, {regionId: controls, childId: 'live-recordings'});
  useDestinationEntryFocus({visible: theme.isTV && !managementActive && !recordingsOpen && managementReturn > 0, intent: 'restore', revision: managementReturn}, {regionId: controls, childId: 'live-owner-open'});
  const empty = !state.loading && state.sources.length === 0;
  // web Live.route: a filter that matches no channel says so and offers the way back to All.
  const filterEmpty = !empty && filterActive && data.total === 0 && !data.channelsError;
  // Show all unmounts the focused button and this whole region; on TV the guide then asks for its own entry
  // (tv-focus 24: the programme on now on the first channel) instead of leaving focus to native recovery.
  const managesServer = managementCurrent();
  // As on web: the Recordings entry is drawn unless the viewer has no sources and no way to set one up; a server that cannot record shows its own unavailable state inside.
  const hasRecordings = !(empty && !managesServer);
  // tablet-play6/7: the Recordings entry carries a red dot while a conflict needs a look. The count is the DVR summary's own,
  // read whenever the guide shows (again on returning from Recordings, and after a recording is scheduled here); TV draws none.
  const recordingState = useSyncExternalStore(recordings.subscribe, recordings.getSnapshot);
  const conflicts = !theme.isTV && hasRecordings ? recordingState.summary?.conflicts ?? 0 : 0;
  useEffect(() => {if (!theme.isTV && hasRecordings && onLive && !recordingsOpen && !ownerOpen) void recordings.loadSummary();}, [recordings, theme.isTV, hasRecordings, onLive, recordingsOpen, ownerOpen, recordingsReturn]);
  const recordingsLabel = conflicts > 0 ? t('next.live.recordingsConflicts', {count: conflicts}) : t('recordings.title');
  // The public destination must be registered by Web and admitted by the handoff's
  // exact public URL validator before a control can offer it.
  const managementUrl = useMemo(() => {
    if (!managesServer) return undefined;
    try {
      const origin = new URL(ports.context.api.baseUrl);
      if (origin.pathname !== '/' || origin.search || origin.hash) return undefined;
      publicTVHandoffAddress(origin.href);
      return publicTVHandoffAddress(new URL('/settings?scope=server&section=live', origin).href).url;
    } catch {return undefined;}
  }, [managesServer, ports.context.api.baseUrl]);
  const openManagement = (action?: 'add' | 'discover') => {
    if (!managementCurrent() || managementOwnsPage()) return;
    if (!theme.isTV) {setInitialOwnerAction(action); setOwnerOpen(true); return;}
    if (managementUrl) setManagement({opening: managementOwner, url: managementUrl});
  };
  // Spec §10 and states 2 (Banner): a stale guide and a server-wide reason are each said once, from the
  // server's own source summaries, as on web (LT-3, LT-4 native half, W2-A10). A quiet line; no focus on TV.
  const serverWide = liveServerWideNotice(selectedSources, managesServer), stale = liveGuideStale(selectedSources);
  return {theme, t, dockTop, serverName: shell.serverName, state, guide, data, ids, compact, screenReader, phone, phoneChrome, expandedTouch,
    insetLeft, insetRight, now, start, view, setView, selected, setSelected, channelMenu, setChannelMenu, channelMenuBusy,
    channelMenuError, setChannelMenuError, channelMenuAnchor, content, setMeasuredWidth, measureContent, contentWidth,
    recording, setRecording, recordError, setRecordError, series, setSeries, toast, setToast, reminders, later,
    laterRecordings, laterRecording, guideReentry, guideReentryRef, tvRegistry, reentryBaseline, scopeKey, reentry,
    management, managementActive, closeManagement, ownerOpen, setOwnerOpen, initialOwnerAction, setInitialOwnerAction,
    recordingsOpen, setRecordingsOpen, setRecordingsReturn, recordingsReturn, managementReturn, opener, selectedOpener,
    list, visible, guideOffset, pull, channelWidth, paneLeft, guideEnd, twoPane, width, rowHeight, rowGap, px, end,
    tvRange, revealTVCell, pan, panPosition, panActive, rulerShift, nowShift, nowPillShift, pastShade, earlierNow, laterNow,
    overscan, clock, goNow, nowX, inNowSpan, open, openChannelMenu, changeChannel, remind, recordLater, askSeries, favorite,
    watch, select, chooseSource, sourceOptions, chooseDay, shortDate, fullDate, dayName, dayTrailing, dayNote, days, daysCount,
    today, currentDay, sortOptions, chooseSort, controls, grid, filters, empty, filterEmpty, filterChips,
    managesServer, hasRecordings, conflicts, recordingsLabel, managementUrl, openManagement, serverWide, stale};
}
