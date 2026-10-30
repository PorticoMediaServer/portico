import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, StandardBackdrop} from '../../kit/artwork';
import {Chip, Focusable, Switch} from '../../kit/controls';
import {CardFrame} from '../../kit/cards/primitives';
import {BottomSheet, Menu, type MenuAction} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {browseType, alpha} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {DownloadsService, TrackedDownloadRequest} from '../../../../../packages/client-core/src/downloads-service';
import {type NativeOfflineDownloadsDevice, type OfflineEntry, type OfflineSettings, type OfflineSnapshot} from '../../platform/offline-downloads';
import type {NativeShellRuntime} from '../../shell/runtime';
import {downloadEntryIdentity, downloadGroups, downloadRequestLines, downloadRowText, downloadStorageSegments, downloadTransferProgress, type NativeDownloadsCopy} from './presentation';
import {useDownloadArtwork} from './useDownloadArtwork';
import {useReadingFacts} from './useReadingFacts';
import {SavedConfirmSheet} from '../saved/SavedForms';
import {createDownloadInventory, createDownloadViewport, downloadWindow} from './inventory';

const t = defaultI18n.t;
const emptyRequests: readonly TrackedDownloadRequest[] = Object.freeze([]);
const noRequests = (): readonly TrackedDownloadRequest[] => emptyRequests;
const noSubscribe = (_listener: () => void) => () => {};
const limits = [0, 5, 10, 25, 50, 100].map(gb => gb * 1073741824);

export type {NativeDownloadsCopy} from './presentation';
type OthersOpening = Readonly<{device: NativeOfflineDownloadsDevice; revision: number; bytes: number}>;
type RenderedOthers = Readonly<{device: NativeOfflineDownloadsDevice; snapshot: OfflineSnapshot; pending: boolean}>;

export type NativeDownloadsScreenProps = Readonly<{
  device: NativeOfflineDownloadsDevice; runtime: NativeShellRuntime; copy: NativeDownloadsCopy;
  back: () => void; openTitle: (entry: OfflineEntry) => void;
  /** Saved's own Downloads tab draws the Saved chrome above this screen. */
  inline?: boolean;
  /** The same authority-local service the route created; its requests are the
   * server's whole-container preparations for this device. */
  service?: Pick<DownloadsService, 'subscribe' | 'getSnapshot'>;
}>;

/** The device adapter publishes only this viewer's native files. No server preparation
 * is ever counted as a file available here, or substituted for a local action. */
export function DownloadsScreen({device, runtime, copy, back, openTitle, service, inline}: NativeDownloadsScreenProps) {
  const theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const snapshot = useSyncExternalStore(device.subscribe, device.getSnapshot);
  const inventory = useMemo(() => createDownloadInventory(device, {
    identity: downloadEntryIdentity,
    membershipKey: entry => [entry.kind, entry.title, entry.addedAt, entry.info],
    entryFence: entry => entry.state === 'ready' ? device.playabilityReason(entry) : null,
    project: entries => {
      const reading = entries.filter(readingEntry), watching = entries.filter(entry => !readingEntry(entry));
      return {groups: downloadGroups(entries), reading, watching: downloadGroups(watching)};
    },
  }), [device]);
  const membership = useSyncExternalStore(inventory.subscribe, inventory.getMembership);
  const viewport = useMemo(createDownloadViewport, [device]), [inventoryTop, setInventoryTop] = useState(0);
  const [failure, setFailure] = useState<unknown>(), [entryMenu, setEntryMenu] = useState<{entry: OfflineEntry; anchor: RefObject<View | null>}>();
  const menu = entryMenu && snapshot.entries.find(entry => downloadEntryIdentity(entry) === downloadEntryIdentity(entryMenu.entry));
  useEffect(() => {if (entryMenu && !menu) setEntryMenu(undefined);}, [entryMenu, menu]);
  const [pickingLimit, setPickingLimit] = useState(false), [pending, setPending] = useState(false);
  const [paneWidth, setPaneWidth] = useState(0);
  const [removingOthers, setRemovingOthers] = useState<OthersOpening>();
  const alive = useRef(true), operation = useRef(0), scroll = useRef<ScrollView>(null), limitRef = useRef<View>(null);
  const removeOthersOpening = useRef<OthersOpening | undefined>(undefined), removeOthersTicket = useRef<object | undefined>(undefined), mounted = useRef(false);
  useEffect(() => {const lifetime = alive, tickets = operation; lifetime.current = true; return () => {lifetime.current = false; tickets.current++;};}, [device]);
  // The latest committed render of this screen: a retained opener from an earlier render
  // must match this device and this exact published snapshot before it may open anything.
  const liveOthers = useRef<RenderedOthers>({device, snapshot, pending});
  useLayoutEffect(() => {liveOthers.current = {device, snapshot, pending};});
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; removeOthersOpening.current = undefined; removeOthersTicket.current = undefined;};}, []);
  // A replaced device (viewer or authority) retires the opening and the C9 operation that
  // described the old one. Other commands keep their own passive lifetime.
  useLayoutEffect(() => {
    removeOthersOpening.current = undefined; setRemovingOthers(undefined);
    if (removeOthersTicket.current) {removeOthersTicket.current = undefined; if (mounted.current) setPending(false);}
    return () => {
      removeOthersOpening.current = undefined;
      if (removeOthersTicket.current) {removeOthersTicket.current = undefined; if (mounted.current) setPending(false);}
    };
  }, [device]);
  // The published other-viewer target changed while its sheet was open: retire that opening.
  useLayoutEffect(() => {
    const opening = removeOthersOpening.current;
    if (opening && (snapshot.othersRevision !== opening.revision || snapshot.otherBytes !== opening.bytes)) {
      removeOthersOpening.current = undefined; setRemovingOthers(undefined);
    }
  }, [snapshot.otherBytes, snapshot.othersRevision]);
  const run = useCallback((work: () => Promise<void>) => {
    const revision = ++operation.current;
    setFailure(undefined); setPending(true);
    void work().catch(error => {if (alive.current && operation.current === revision) {setFailure(error); runtime.reportError(error);}})
      .finally(() => {if (alive.current && operation.current === revision) setPending(false);});
  }, [runtime]);
  useEffect(() => {run(device.open);}, [device, run]);
  const save = (settings: Partial<OfflineSettings>) => run(() => device.setSettings(settings));
  // states.html 2: removing other profiles' files is exact and final. The drawn target is
  // the opaque set revision and the size shown; a successor target retires the sheet rather
  // than adopting it, and Cancel/confirm stay bound to the opening that drew it.
  const openRemoveOthers = () => {
    // The click must belong to the render that is actually on screen: a retained opener from
    // another device renders against this witness and refuses before any setter runs.
    const witness = liveOthers.current;
    if (!mounted.current || witness.pending || pending) return;
    if (witness.device !== device || witness.snapshot !== snapshot) return;
    if (device.getSnapshot() !== snapshot) return;
    if (snapshot.otherBytes <= 0) return;
    const revision = snapshot.othersRevision;
    if (revision === undefined || revision < 1) return;
    const opening: OthersOpening = {device, revision, bytes: snapshot.otherBytes};
    removeOthersOpening.current = opening; setRemovingOthers(opening);
  };
  const closeRemoveOthers = () => {
    const opening = removingOthers;
    if (!opening || removeOthersOpening.current !== opening) return;
    removeOthersOpening.current = undefined; setRemovingOthers(undefined);
  };
  // C9's own operation ticket: only this ticket on this exact device may publish its result
  // or clear the busy flag. The shared passive lifetime and operation counter stay untouched.
  const runRemoveOthers = (opening: OthersOpening) => {
    const ticket = {}, ownerDevice = opening.device;
    removeOthersTicket.current = ticket;
    setFailure(undefined); setPending(true);
    const current = () => mounted.current && removeOthersTicket.current === ticket && liveOthers.current.device === ownerDevice;
    void ownerDevice.removeOthers({revision: opening.revision, bytes: opening.bytes})
      .catch(error => {if (current()) {setFailure(error); runtime.reportError(error);}})
      .finally(() => {if (!current()) return; removeOthersTicket.current = undefined; setPending(false);});
  };
  const confirmRemoveOthers = () => {
    const opening = removingOthers;
    if (!opening || !mounted.current || removeOthersOpening.current !== opening || opening.device !== device) return;
    if (liveOthers.current.device !== opening.device) return;
    removeOthersOpening.current = undefined; setRemovingOthers(undefined);
    runRemoveOthers(opening);
  };

  const storage = downloadStorageSegments(snapshot);
  const requests = useSyncExternalStore(service ? service.subscribe : noSubscribe, service ? () => service.getSnapshot().requests : noRequests);
  const waiting = snapshot.pendingDownloads ?? [];
  const empty = snapshot.ready && snapshot.entries.length === 0 && requests.length === 0 && waiting.length === 0;
  // tablet-read12 draws Saved › Downloads at the expanded width: what the device
  // holds and what is left, then the profile's actual reading publications and
  // everything else as two-column lists. Phone and medium keep today's page.
  const expanded = !theme.isTV && dimensions.width >= 1024;
  const readingEntries = membership.reading;
  // A reading page keeps the full-width lists and rules below (tablet-read12).
  const rulesBeside = expanded && !empty && readingEntries.length === 0;
  let readingBytes = 0, watchingBytes = 0;
  for (const entry of snapshot.entries) {
    const bytes = entry.state === 'ready' ? entry.bytes : entry.bytesDone;
    if (readingEntry(entry)) readingBytes += bytes; else watchingBytes += bytes;
  }
  const heldBytes = Math.max(snapshot.usedBytes, readingBytes + watchingBytes);
  // tablet-read12's measured reading page is an expanded, non-phone reading page. 628 is
  // an inferred usable-row minimum (48 grid gutter + 20 column gap + 2 x 280), not a
  // frame measurement; below it the existing page is left exactly as it was.
  const readingTablet = expanded && theme.platform !== 'phone' && readingEntries.length > 0 && paneWidth >= 628;
  const readingColumn = readingTablet ? Math.max(0, (paneWidth - 48 - 20) / 2) : undefined;
  // The tablet split reserves only its real 2px inter-segment gap from the known inner bar
  // width, then applies the existing watching/held and reading/held ratios. Any remainder
  // stays unrepresented; the default percentage bar is untouched.
  const readingBarInner = readingTablet ? Math.max(0, paneWidth - 64 - 40) : 0;
  const readingBarGap = readingTablet && watchingBytes > 0 && readingBytes > 0 ? 2 : 0;
  const readingBarWatching = readingTablet ? (readingBarInner - readingBarGap) * watchingBytes / Math.max(1, heldBytes) : 0;
  const readingBarReading = readingTablet ? (readingBarInner - readingBarGap) * readingBytes / Math.max(1, heldBytes) : 0;
  const requestText = (tracked: TrackedDownloadRequest): readonly string[] => downloadRequestLines(tracked.request).map(line =>
    line.key === 'downloads.requestProgress' ? t(line.key, line.values as {ready: number; total: number})
      : line.key === 'downloads.requestFailed' ? t(line.key, line.values as {failed: number}) : t(line.key));
  const ruleList = <DownloadsRules settings={snapshot.settings} pending={pending} copy={copy} save={save} limitRef={limitRef} onLimit={() => setPickingLimit(true)}/>;
  const otherNotice = snapshot.otherBytes > 0 && <View style={{marginHorizontal: 16, marginTop: 14, paddingLeft: 14, paddingRight: 12, paddingVertical: 12, borderRadius: 12, borderWidth: 1, borderColor: alpha(theme.colors.text, .08), flexDirection: 'row', gap: 10, alignItems: 'center'}}>
    <Icon name="i-info" size={18} color={theme.colors.textSecondary}/><Text style={[browseType(theme, 13.5, 400), {color: theme.colors.textSecondary, flex: 1}]}>{t(copy.otherProfiles, {size: defaultI18n.bytes(snapshot.otherBytes)})}</Text>
    <Chip testID="downloads-remove-other-profiles" disabled={pending || snapshot.otherBytes <= 0} onPress={openRemoveOthers}>{t('action.remove')}</Chip>
  </View>;
  const firstMenuAction = useRef<View>(null);
  const menuItems: MenuAction[] = menu ? [
    ...(menu.state !== 'ready' ? [{id: 'resume', label: t('action.tryAgain'), icon: 'i-retry' as const, onSelect: () => run(() => device.resume(menu))}] : []),
    {id: 'title', label: t('web.downloads.goToTitle'), icon: 'i-film', onSelect: () => openTitle(menu)},
    {id: 'remove', label: t(copy.removeFromPhone), icon: 'i-trash', destructive: true, onSelect: () => run(() => device.remove(menu))},
  ] : [];
  const openMenu = useCallback((entry: OfflineEntry, anchor: RefObject<View | null>) => setEntryMenu({entry, anchor}), []);
  const playEntry = useCallback((entry: OfflineEntry) => {if (device.openOffline) run(() => device.openOffline!(entry));}, [device, run]);
  const retryEntry = useCallback((entry: OfflineEntry) => run(() => device.resume(entry)), [device, run]);
  return <View testID="native-downloads-screen" onLayout={event => setPaneWidth(event.nativeEvent.layout.width)} style={{flex: 1}}><StandardBackdrop/>
    {!inline && <View style={{height: Math.max(96, insets.top + 48), paddingTop: Math.max(50, insets.top), paddingHorizontal: 10, justifyContent: 'center'}}>
      <Focusable testID="downloads-back" accessibilityLabel={t('saved.title')} onPress={back} style={{height: 38, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', paddingRight: 6, marginLeft: -4}}>
        <Icon name="i-left" size={24} color={theme.colors.textSecondary}/><Text style={[browseType(theme, 16, 600), {color: theme.colors.textSecondary}]}>{t('saved.title')}</Text>
      </Focusable>
      <Text pointerEvents="none" accessibilityRole="header" style={[browseType(theme, 17, 700), {position: 'absolute', left: 80, right: 80, bottom: 13, textAlign: 'center', color: theme.colors.text}]}>{t('downloads.title')}</Text>
    </View>}
    <ScrollView ref={scroll} style={{flex: 1}} scrollEventThrottle={64}
      onLayout={event => viewport.update({...viewport.getSnapshot(), height: event.nativeEvent.layout.height})}
      onScroll={event => viewport.update({...viewport.getSnapshot(), offset: event.nativeEvent.contentOffset.y})} contentContainerStyle={{paddingTop: inline ? 8 : 0, paddingBottom: 28}}>
      {!snapshot.supported ? <StateView title={t('downloads.unavailableOnDevice')}/> : <>
        {failure && <StateView {...runtime.error(failure, () => run(device.open))}/>}
        {snapshot.error && <StateView title={snapshot.error} tone="error"/>}
        {empty && <View style={{paddingTop: 58, paddingHorizontal: 24, alignItems: 'center'}}>
          <View style={{width: 54, height: 54, borderRadius: 27, backgroundColor: alpha(theme.colors.text, .08), alignItems: 'center', justifyContent: 'center', marginBottom: 18}}><Icon name="i-dl" size={26} color={theme.colors.text}/></View>
          <Text accessibilityRole="header" style={[browseType(theme, 30, 800, -.035, 32.4), {color: theme.colors.text, textAlign: 'center', marginBottom: 10}]}>{t('next.states.native.noDownloads')}</Text>
          <Text style={[browseType(theme, 15, 400, 0, 22.5), {color: theme.colors.textSecondary, textAlign: 'center', marginBottom: 16}]}>{t(copy.emptyBody)}</Text>
        </View>}
        {!empty && snapshot.ready && <View style={readingTablet ? {marginHorizontal: 32, marginTop: 22, paddingVertical: 16, paddingHorizontal: 20, borderRadius: 16, backgroundColor: theme.colors.raised}
          : {marginHorizontal: 16, marginTop: 4, padding: 14, borderRadius: 14, backgroundColor: theme.colors.raised}}>
          <View style={{flexDirection: 'row', gap: 12, alignItems: 'baseline'}}>
          <Text style={[browseType(theme, readingTablet ? 17 : 14.5, readingTablet ? 800 : 700), {color: theme.colors.text}]}>{t('downloads.used', {size: defaultI18n.bytes(snapshot.usedBytes)})}</Text>
          <Text style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary}]}>{t(expanded ? 'downloads.freeOnDevice' : copy.freeOnPhone, {size: defaultI18n.bytes(snapshot.freeBytes)})}</Text>
          </View>
          {expanded ? (heldBytes > 0 && <>
            <View testID="downloads-storage-split" style={{height: readingTablet ? 7 : 6, borderRadius: readingTablet ? 4 : 3, backgroundColor: theme.colors.raisedBright,
              marginTop: readingTablet ? 12 : 10, marginBottom: readingTablet ? 10 : 0, overflow: 'hidden', flexDirection: 'row', gap: readingBarGap}}>
              <View style={{height: readingTablet ? 7 : 6, width: readingTablet ? readingBarWatching : `${100 * watchingBytes / heldBytes}%`, backgroundColor: theme.colors.accentStrong}}/>
              <View style={{height: readingTablet ? 7 : 6, width: readingTablet ? readingBarReading : `${100 * readingBytes / heldBytes}%`, backgroundColor: theme.colors.amber}}/>
            </View>
            <View style={{flexDirection: 'row', gap: readingTablet ? 20 : 18, marginTop: readingTablet ? 0 : 10, flexWrap: 'wrap'}}>
              <DownloadsStorageKey metric={readingTablet} color={theme.colors.accentStrong} label={t('next.live.watching')} size={watchingBytes}/>
              <DownloadsStorageKey metric={readingTablet} color={theme.colors.amber} label={t('next.settingsSpec.account.reading.title')} size={readingBytes}/>
            </View>
          </>) : storage && <View testID="downloads-storage-bar" style={{height: 6, borderRadius: 3, backgroundColor: theme.colors.raisedBright, marginTop: 10, overflow: 'hidden', flexDirection: 'row'}}>
            <View style={{height: 6, width: `${100 * storage.downloads}%`, backgroundColor: theme.colors.accentStrong}}/>
            <View style={{height: 6, width: `${100 * storage.other}%`, backgroundColor: alpha(theme.colors.accentStrong, .4)}}/>
          </View>}
        </View>}
        {requests.length > 0 && <View testID="downloads-requests" style={{marginTop: 18, marginHorizontal: 16}}>
          <Text accessibilityRole="header" style={[browseType(theme, 13.5, 700), {color: theme.colors.textSecondary, marginBottom: 8}]}>{t('downloads.requestsTitle')}</Text>
          <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
            {requests.map((tracked, index) => <View key={tracked.request.requestId} testID={`downloads-request-${tracked.request.requestId}`}
              style={{paddingHorizontal: 14, paddingVertical: 12, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .45)}}>
              <Text numberOfLines={1} style={[browseType(theme, 15, 700), {color: theme.colors.text}]}>{tracked.title}</Text>
              <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: 10, marginTop: 3}}>{requestText(tracked).map((text, index) =>
                <Text key={index} style={[browseType(theme, 13, index === 0 ? 600 : 400), {color: theme.colors.textTertiary}]}>{text}</Text>)}</View>
            </View>)}
          </View>
        </View>}
        {waiting.length > 0 && <View testID="downloads-waiting" style={{marginTop: 18, marginHorizontal: 16, backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
          {waiting.map((entry, index) => <View key={entry.id} testID={`downloads-waiting-${entry.id}`}
            style={{paddingHorizontal: 14, paddingVertical: 12, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .45), flexDirection: 'row', alignItems: 'center', gap: 12}}>
            <View style={{flex: 1, minWidth: 0}}>
              <Text numberOfLines={1} style={[browseType(theme, 15, 700), {color: theme.colors.text}]}>{entry.title}</Text>
              {!!entry.subtitle && <Text style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary, marginTop: 3}]}>{entry.subtitle}</Text>}
              <Text style={[browseType(theme, 13, 400), {color: theme.colors.amber, marginTop: 3}]}>{entry.reason}</Text>
            </View>
            {device.cancelPending && <Chip testID={`downloads-cancel-${entry.id}`} disabled={pending} onPress={() => run(() => device.cancelPending!(entry.id))}>{t('action.cancel')}</Chip>}
          </View>)}
        </View>}
        <View onLayout={event => setInventoryTop(event.nativeEvent.layout.y)} style={rulesBeside ? {flexDirection: 'row', alignItems: 'flex-start'} : undefined}>
          {!empty && <DownloadInventoryWindow inventory={inventory} membership={membership} viewport={viewport} top={inventoryTop}
            expanded={expanded} readingTablet={readingTablet} readingColumn={readingColumn} rulesBeside={rulesBeside} paneWidth={paneWidth}
            device={device} runtime={runtime} copy={copy} menuId={menu ? downloadEntryIdentity(menu) : undefined}
            onMenu={openMenu} onPlay={device.openOffline ? playEntry : undefined} onRetry={retryEntry}/>}
          <View style={rulesBeside ? {width: 360} : expanded && !empty ? {marginTop: 10} : undefined}>{ruleList}{otherNotice}</View>
        </View>
      </>}
    </ScrollView>
    {dimensions.width < 600 && !theme.isTV ? <BottomSheet testID="downloads-entry-menu" title={undefined} open={!!menu} onOpenChange={open => {if (!open) setEntryMenu(undefined);}}
      initialFocusRef={firstMenuAction} returnFocusRef={entryMenu?.anchor} accessibilityLabel={menu?.title} headerContent={menu && <DownloadMenuContext entry={menu} copy={copy}/>}
      showClose={false} fitContent scrollable={false}>
      <View style={{paddingBottom: Math.max(26, insets.bottom)}}>{menuItems.map((action, index) => <Focusable key={action.id} ref={index === 0 ? firstMenuAction : undefined} testID={`downloads-entry-menu-item-${action.id}`}
        onPress={() => {setEntryMenu(undefined); action.onSelect();}} style={{height: 54, paddingHorizontal: 20, flexDirection: 'row', alignItems: 'center', gap: 14}}>
        {action.icon && <Icon name={action.icon} size={22} color={action.destructive ? theme.colors.record : theme.colors.textSecondary}/>}
        <Text style={[browseType(theme, 16, 600), {color: action.destructive ? theme.colors.record : theme.colors.text}]}>{action.label}</Text>
      </Focusable>)}</View>
    </BottomSheet> : <Menu testID="downloads-entry-menu" title={undefined} open={!!menu} onOpenChange={open => {if (!open) setEntryMenu(undefined);}} anchorRef={entryMenu?.anchor} returnFocusRef={entryMenu?.anchor} accessibilityLabel={menu?.title}
      context={menu && <DownloadMenuContext entry={menu} copy={copy}/>} items={menuItems} presentation={theme.isTV ? 'sideSheet' : 'anchored'}/>}
    <Menu testID="downloads-storage-limit" open={pickingLimit} onOpenChange={setPickingLimit} anchorRef={limitRef} returnFocusRef={limitRef} title={t('downloads.storageLimit')}
      items={limits.map(value => ({id: String(value), label: value ? defaultI18n.bytes(value) : t('downloads.noLimit'), selected: snapshot.settings.limitBytes === value,
        onSelect: () => save({limitBytes: value})}))} presentation={dimensions.width < 600 ? 'sheet' : 'anchored'}/>
    <SavedConfirmSheet open={!!removingOthers} title={t('confirm.removeOthers.title')} action={t('action.remove')} destructive busy={pending}
      body={removingOthers ? `${t('downloads.othersOnDevice', {size: defaultI18n.bytes(removingOthers.bytes)})} ${t('confirm.removeOthers.body')}` : ''}
      testID="downloads-remove-other-profiles-confirm" onClose={closeRemoveOthers} onConfirm={confirmRemoveOthers}/>
  </View>;
}

function readingEntry(entry: OfflineEntry): boolean {
  const kind = entry.info?.kind ?? entry.kind; return kind === 'ebook' || kind === 'comic';
}
type DownloadMembership = Readonly<{groups: ReturnType<typeof downloadGroups>; reading: readonly OfflineEntry[]; watching: ReturnType<typeof downloadGroups>}>;
type DownloadInventory = ReturnType<typeof createDownloadInventory<OfflineEntry, DownloadMembership>>;
type WindowRow = Readonly<{key: string; kind: 'section' | 'group' | 'entries'; estimate: number; label?: string; count?: number;
  metric?: boolean; reading?: boolean; ids?: readonly string[]; first?: boolean; last?: boolean}>;

/** One bounded native window inside the page's existing scroll owner. The same measured
 * geometry retains compact rounded groups and the tablet's two-column rows/rules split. */
const DownloadInventoryWindow = React.memo(function DownloadInventoryWindow({inventory, membership, viewport, top, expanded, readingTablet, readingColumn,
  rulesBeside, paneWidth, device, runtime, copy, menuId, onMenu, onPlay, onRetry}: {
  inventory: DownloadInventory; membership: DownloadMembership; viewport: ReturnType<typeof createDownloadViewport>; top: number;
  expanded: boolean; readingTablet: boolean; readingColumn?: number; rulesBeside: boolean; paneWidth: number;
  device: NativeOfflineDownloadsDevice; runtime: NativeShellRuntime; copy: NativeDownloadsCopy; menuId?: string;
  onMenu: (entry: OfflineEntry, anchor: RefObject<View | null>) => void; onPlay?: (entry: OfflineEntry) => void; onRetry: (entry: OfflineEntry) => void;
}) {
  const theme = useTheme(), visible = useSyncExternalStore(viewport.subscribe, viewport.getSnapshot);
  const [focused, setFocused] = useState<string>(), [measureVersion, setMeasureVersion] = useState(0), measures = useRef(new Map<string, number>());
  const layoutKey = `${paneWidth}:${expanded}:${readingTablet}:${rulesBeside}`, measuredLayout = useRef(layoutKey);
  if (measuredLayout.current !== layoutKey) {measuredLayout.current = layoutKey; measures.current.clear();}
  const rows = useMemo(() => {
    const result: WindowRow[] = [];
    const append = (key: string, entries: readonly OfflineEntry[], columns: number, reading = false) => {
      for (let index = 0; index < entries.length; index += columns) result.push({key: `${key}:${downloadEntryIdentity(entries[index]!)}`,
        kind: 'entries', ids: entries.slice(index, index + columns).map(downloadEntryIdentity), reading, metric: reading && readingTablet,
        first: index === 0, last: index + columns >= entries.length, estimate: reading && readingTablet ? 97 : 88});
    };
    if (expanded) {
      if (membership.reading.length) {
        result.push({key: 'section:reading', kind: 'section', label: t('next.settingsSpec.account.reading.title'), count: membership.reading.length, metric: readingTablet, estimate: readingTablet ? 58 : 50});
        append('reading', membership.reading, 2, true);
      }
      if (membership.watching.length) result.push({key: 'section:watching', kind: 'section', label: t('next.live.watching'), count: membership.watching.length, estimate: 50});
      for (const group of membership.watching) {
        result.push({key: `group:${group.key}`, kind: 'group', label: group.title, count: group.episodeCount, estimate: group.title ? 40 : 8});
        append(group.key, group.entries, rulesBeside ? 1 : 2);
      }
    } else for (const group of membership.groups) {
      if (group.title) result.push({key: `group:${group.key}`, kind: 'group', label: group.title, count: group.episodeCount, estimate: 46});
      append(group.key, group.entries, 1);
    }
    return result;
  }, [membership, expanded, readingTablet, rulesBeside]);
  const offsets = useMemo(() => {
    const next = [0]; for (const row of rows) next.push(next[next.length - 1]! + (measures.current.get(row.key) ?? row.estimate)); return next;
    // Measurements mutate in place; these keys invalidate offsets after layout/height changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, layoutKey, measureVersion]);
  const range = downloadWindow(offsets, visible.offset - top, visible.height);
  const indices = new Set(Array.from({length: range.end - range.start}, (_, index) => range.start + index));
  for (const id of [menuId, focused]) if (id) {const index = rows.findIndex(row => row.ids?.includes(id)); if (index >= 0) indices.add(index);}
  const mounted = [...indices].sort((a, b) => a - b);
  return <View testID="downloads-inventory-window" style={{height: offsets[offsets.length - 1], ...(rulesBeside ? {flex: 1, minWidth: 0} : {width: '100%'})}}>
    {mounted.map(index => {
      const row = rows[index]!;
      return <View key={row.key} onLayout={event => {
        const height = event.nativeEvent.layout.height;
        if (height > 0 && Math.abs(height - (measures.current.get(row.key) ?? row.estimate)) > .5) {measures.current.set(row.key, height); setMeasureVersion(value => value + 1);}
      }} style={{position: 'absolute', left: 0, right: 0, top: offsets[index]}}>
        {row.kind === 'section' ? <View testID={row.key === 'section:reading' ? 'downloads-reading' : 'downloads-watching'} style={{paddingTop: row.metric ? 26 : 24}}>
          <DownloadsSectionHeading metric={row.metric} label={row.label!} count={row.count!}/>
        </View> : row.kind === 'group' ? row.label ? <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10,
          paddingTop: expanded ? 16 : 20, marginHorizontal: 20, paddingBottom: expanded ? 4 : 8}}>
          <Text accessibilityRole="header" style={[browseType(theme, expanded ? 15 : 13.5, 700), {color: theme.colors.textSecondary}]}>{row.label}</Text>
          {row.count !== undefined && <Text style={[browseType(theme, expanded ? 12.5 : 13.5, expanded ? 600 : 700), {color: theme.colors.textTertiary}]}>{t('count.episodes', {count: row.count})}</Text>}
        </View> : <View style={{height: 8}}/> : <View style={expanded ? row.metric
          ? {flexDirection: 'row', paddingHorizontal: 24, columnGap: 20}
          : {flexDirection: 'row', marginHorizontal: 14}
          : {marginHorizontal: 16, backgroundColor: theme.colors.raised, borderTopLeftRadius: row.first ? 14 : 0, borderTopRightRadius: row.first ? 14 : 0,
            borderBottomLeftRadius: row.last ? 14 : 0, borderBottomRightRadius: row.last ? 14 : 0, overflow: 'hidden'}}>
          {row.ids!.map(id => <View key={id} style={expanded ? row.metric ? {width: readingColumn}
            : {width: rulesBeside ? '100%' : '50%', paddingHorizontal: 6} : undefined}>
            <View style={expanded ? {borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.lineStrong, .45)} : undefined}>
              <LiveDownloadRow id={id} inventory={inventory} metric={row.metric} device={device} runtime={runtime} copy={copy}
                separator={!expanded && !row.first} menuOpen={menuId === id} onMenu={onMenu} onPlay={onPlay} onRetry={onRetry} onFocus={setFocused}/>
            </View>
          </View>)}
        </View>}
      </View>;
    })}
  </View>;
});

const LiveDownloadRow = React.memo(function LiveDownloadRow({id, inventory, device, runtime, copy, separator, menuOpen, onMenu, onPlay, onRetry, metric, onFocus}: {
  id: string; inventory: DownloadInventory; device: NativeOfflineDownloadsDevice; runtime: NativeShellRuntime; copy: NativeDownloadsCopy;
  separator: boolean; menuOpen: boolean; metric?: boolean; onMenu: (entry: OfflineEntry, anchor: RefObject<View | null>) => void;
  onPlay?: (entry: OfflineEntry) => void; onRetry: (entry: OfflineEntry) => void; onFocus: (id: string) => void;
}) {
  const read = useCallback(() => inventory.getEntry(id), [inventory, id]);
  const selected = useSyncExternalStore(inventory.subscribe, read);
  useEffect(() => () => inventory.release(id), [inventory, id]);
  if (!selected) return null;
  const entry = selected.entry;
  return <DownloadRow entry={entry} device={device} runtime={runtime} copy={copy} separator={separator} menuOpen={menuOpen} metric={metric}
    onFocus={() => onFocus(id)} onMenu={anchor => onMenu(entry, anchor)} onPlay={onPlay ? () => onPlay(entry) : undefined} onRetry={() => onRetry(entry)}/>;
});

function DownloadsSectionHeading({label, count, metric = false}: {label: string; count: number; metric?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap: metric ? 12 : 10, marginHorizontal: metric ? 32 : 20, marginBottom: 6}}>
    <Text accessibilityRole="header" style={[browseType(theme, metric ? 21 : 17, 700), {color: theme.colors.text}]}>{label}</Text>
    <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary}]}>{defaultI18n.t('count.titles', {count})}</Text>
  </View>;
}

function DownloadsStorageKey({color, label, size, metric = false}: {color: string; label: string; size: number; metric?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: metric ? 7 : 6}}>
    <View style={{width: metric ? 9 : 7, height: metric ? 9 : 7, borderRadius: metric ? 2 : 4, backgroundColor: color}}/>
    <Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{label}</Text>
    <Text style={[browseType(theme, 13, metric ? 600 : 400), {color: theme.colors.textTertiary}]}>{defaultI18n.bytes(size)}</Text>
  </View>;
}

function DownloadsRules({settings, pending, copy, save, onLimit, limitRef}: {settings: OfflineSettings; pending: boolean; copy: NativeDownloadsCopy; save: (settings: Partial<OfflineSettings>) => void; onLimit: () => void; limitRef: RefObject<View | null>}) {
  const theme = useTheme();
  const rows = [
    {field: 'wifiOnly', label: 'downloads.wifiOnly', help: 'downloads.wifiOnlyHelp'},
    {field: 'deleteWatched', label: 'downloads.removeAfterWatching', help: 'downloads.removeAfterWatchingHelp'},
    {field: 'autoNext', label: 'downloads.nextEpisode', help: 'downloads.nextEpisodeHelp'},
  ] as const;
  const label = (title: string, help: string) => <View style={{flex: 1, minWidth: 0}}><Text style={[browseType(theme, 15.5, 600), {color: theme.colors.text}]}>{title}</Text><Text style={[browseType(theme, 12.5, 500, 0, 17.5), {color: theme.colors.textTertiary, marginTop: 2}]}>{help}</Text></View>;
  return <>
    <Text accessibilityRole="header" style={[browseType(theme, 13.5, 700), {color: theme.colors.textTertiary, marginTop: 26, marginHorizontal: 20, marginBottom: 8}]}>{t('downloads.rules')}</Text>
    <View style={{marginHorizontal: 16, borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>
      {rows.map((row, index) => <View key={row.field} style={{position: 'relative', flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 48, paddingVertical: 8, paddingHorizontal: 14}}>
        {index > 0 && <View style={{position: 'absolute', top: 0, left: 56, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}
        {label(t(row.label), t(row.help))}<Switch testID={`downloads-rule-${row.field}`} accessibilityLabel={t(row.label)} checked={settings[row.field]} disabled={pending}
          variant="phone" onCheckedChange={value => save({[row.field]: value})}/>
      </View>)}
      <Focusable ref={limitRef} testID="downloads-rule-limitBytes" accessibilityLabel={t('downloads.storageLimit')} disabled={pending} onPress={onLimit}
        style={{position: 'relative', flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 48, paddingVertical: 8, paddingHorizontal: 14}}>
        <View style={{position: 'absolute', top: 0, left: 56, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>
        {label(t('downloads.storageLimit'), t(copy.storageLimitHelp))}
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 2}}><Text style={[browseType(theme, 14.5, 600), {color: theme.colors.textTertiary}]}>{settings.limitBytes ? defaultI18n.bytes(settings.limitBytes) : t('downloads.noLimit')}</Text><Icon name="i-right" size={18} color={theme.colors.lineStrong}/></View>
      </Focusable>
    </View>
  </>;
}

function localArtwork(entry: OfflineEntry) {
  return entry.artworkUrl ? {uri: entry.artworkUrl, cacheKey: JSON.stringify([downloadEntryIdentity(entry), entry.artworkUrl])} : undefined;
}

function DownloadMenuContext({entry, copy}: {entry: OfflineEntry; copy: NativeDownloadsCopy}) {
  const theme = useTheme(), progress = downloadTransferProgress(entry);
  const percent = progress === undefined ? undefined : Math.round(progress * 100);
  return <>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 20, paddingTop: 2, paddingBottom: 12, borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.lineStrong, .45)}}>
      <Artwork source={localArtwork(entry)} width={36} height={54} style={{borderRadius: 5}}/>
      <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[browseType(theme, 16, 700), {color: theme.colors.text}]}>{entry.title}</Text>
        {entry.state === 'failed' && percent !== undefined && <Text style={[browseType(theme, 13, 600), {color: theme.colors.record, marginTop: 2}]}>{t(copy.stoppedAt, {percent})}</Text>}
      </View>
    </View>
    {entry.state === 'failed' && percent !== undefined && <Text style={[browseType(theme, 13, 400, 0, 19.5), {color: theme.colors.textTertiary, marginTop: 10, marginHorizontal: 20, marginBottom: 2}]}>{t(copy.stoppedHelp, {percent})}</Text>}
  </>;
}

function DownloadRow({entry, device, runtime, copy, separator, menuOpen, onMenu, onPlay, onRetry, metric = false, onFocus}: {entry: OfflineEntry; device: NativeOfflineDownloadsDevice; runtime: NativeShellRuntime; copy: NativeDownloadsCopy; separator: boolean; menuOpen: boolean; onMenu: (anchor: RefObject<View | null>) => void; onPlay?: () => void; onRetry: () => void; metric?: boolean; onFocus?: () => void}) {
  const theme = useTheme(), blocked = entry.state === 'ready' ? device.playabilityReason(entry) : null, opener = useRef<View>(null);
  const facts = useDownloadArtwork(device, entry), reading = useReadingFacts(device, entry, entry.state === 'ready' && !blocked);
  const text = downloadRowText(entry, facts, blocked, copy, runtime.text, reading);
  const menu = () => onMenu(opener);
  const playable = entry.state === 'ready' && !blocked, progress = downloadTransferProgress(entry);
  const backdrop = entry.info?.kind === 'episode' ? facts?.backdropUrl : undefined;
  const artwork = backdrop ? {uri: backdrop, cacheKey: JSON.stringify([downloadEntryIdentity(entry), backdrop])} : localArtwork(entry);
  return <View style={{position: 'relative', paddingVertical: 9, paddingLeft: metric ? 8 : 14, paddingRight: metric ? 8 : 12, flexDirection: 'row', alignItems: 'center', gap: metric ? 14 : 13,
    ...(metric ? {minHeight: 92, borderRadius: 14} : {})}}>
    {separator && <View style={{position: 'absolute', top: 0, left: 74, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}
    <View style={{flex: 1, minWidth: 0}}>
      <CardFrame title={entry.title} onFocus={onFocus} menuAnchorRef={opener} moreExpanded={menuOpen} testID={`download-${encodeURIComponent(entry.id)}`}
        onNavigate={playable && onPlay ? onPlay : menu} onMore={menu} pressableStyle={{flexDirection: 'row', alignItems: 'center', gap: metric ? 14 : 13}}>
        {() => <>
          <Artwork source={artwork} width={metric ? 52 : 46} height={metric ? 78 : 69} zoom={backdrop ? 2.2 : 1} saturation={backdrop ? .55 : 1} brightness={backdrop ? .45 : 1} style={{borderRadius: metric ? 5 : 6}}/>
          <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[browseType(theme, metric ? 15.5 : 15, 700), {color: theme.colors.text}]}>{entry.title}</Text>
            {!!text.metadata.length && <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, marginTop: metric ? 3 : 2}}>{text.metadata.map((value, index) => <Text key={index} numberOfLines={1} style={[browseType(theme, metric ? 13 : 12.5, metric ? 600 : 400, 0, metric ? 21 : 17.5), {color: theme.colors.textTertiary}]}>{value}</Text>)}</View>}
            {!!text.reason && <Text style={[browseType(theme, 12.5, 400, 0, 17.5), {color: text.stopped ? theme.colors.record : text.warning ? theme.colors.amber : theme.colors.textTertiary, marginTop: 2}]}>{text.reason}</Text>}
            {entry.state === 'downloading' && !entry.waiting && progress !== undefined && <><Text style={[browseType(theme, 12.5, 400, 0, 17.5), {color: theme.colors.textTertiary, marginTop: 2}]}>{t(copy.downloading, {percent: Math.round(100 * progress)})}</Text><View style={{height: 3, backgroundColor: theme.colors.raisedBright, marginTop: metric ? 8 : 7, borderRadius: 2, overflow: 'hidden'}}><View style={{height: 3, width: `${100 * progress}%`, backgroundColor: theme.colors.accentStrong}}/></View></>}
            {text.stopped && <View style={{height: 37}}/>}
          </View>
        </>}
      </CardFrame>
      {text.stopped && <Chip testID={`download-retry-${encodeURIComponent(entry.id)}`} icon="i-retry" iconSize={14} onPress={onRetry} style={{position: 'absolute', bottom: 0, left: 59, height: 30, paddingHorizontal: 11}} textStyle={browseType(theme, 13, 700)}>{t('action.tryAgain')}</Chip>}
    </View>
    <Focusable testID={`download-action-${encodeURIComponent(entry.id)}`} accessibilityLabel={t(playable ? (entry.kind === 'ebook' || entry.kind === 'comic' ? 'next.cards.read' : 'action.play') : 'card.more', {title: entry.title})}
      disabled={playable && !onPlay} onPress={playable ? onPlay : menu} style={{width: metric ? 44 : 34, height: metric ? 44 : 34, borderRadius: metric ? 22 : 17, alignItems: 'center', justifyContent: 'center', backgroundColor: playable ? theme.colors.text : undefined}}>
      <Icon name={playable ? (entry.kind === 'ebook' || entry.kind === 'comic' ? 'i-book' : 'i-play') : 'i-more'} size={playable ? 16 : 20} color={playable ? theme.colors.ink : theme.colors.textTertiary}/>
    </Focusable>
  </View>;
}
