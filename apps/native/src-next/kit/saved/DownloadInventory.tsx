import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {TrackedDownloadRequest} from '../../../../../packages/client-core/src/downloads-service';
import {Artwork} from '../artwork';
import {Chip, Focusable, Switch} from '../controls';
import {CardFrame} from '../cards/primitives';
import {BottomSheet, Menu, type MenuAction} from '../overlays';
import {Icon} from '../Icon';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';
import type {NativeOfflineDownloadsDevice, OfflineEntry, OfflineSettings, OfflineSnapshot} from '../../platform/offline-downloads';
import type {NativeShellRuntime} from '../../shell/runtime';
import {downloadEntryIdentity, downloadRequestLines, downloadRowText, downloadTransferProgress, type NativeDownloadsCopy} from '../../screens/downloads/presentation';
import {useDownloadArtwork} from '../../screens/downloads/useDownloadArtwork';
import {useReadingFacts} from '../../screens/downloads/useReadingFacts';
import {type createDownloadViewport, downloadWindow} from '../../screens/downloads/inventory';
import type {DownloadInventory, DownloadMembership} from '../../pages/shared/saved/useDownloads';

const t = defaultI18n.t;
const readingGutter = 24, readingGap = 20, readingColumnMin = 280;

export function useDownloadsLayout(nativeCopy: NativeDownloadsCopy) {
  const theme = useTheme(), {width} = useWindowDimensions();
  const [paneWidth, setPaneWidth] = useState(0), [inventoryTop, setInventoryTop] = useState(0);
  const expanded = !theme.isTV && width >= 1024;
  const sheetMenu = !theme.isTV && width < 600;
  const tablet = theme.form === 'medium' || theme.form === 'expanded';
  const copy: NativeDownloadsCopy = tablet ? {...nativeCopy, storageLimitHelp: 'next.saved.tablet.storageLimitHelp',
    freeOnPhone: 'downloads.freeOnDevice', removeFromPhone: 'next.saved.tablet.remove',
    goneFromServer: 'next.saved.tablet.gone', otherProfiles: 'downloads.othersOnDevice'} : nativeCopy;
  return {expanded, sheetMenu, paneWidth, setPaneWidth, inventoryTop, setInventoryTop, copy,
    inventoryLayout: (empty: boolean, hasReading: boolean) => ({
      readingTablet: expanded && theme.platform !== 'phone' && hasReading && paneWidth >= 2 * (readingColumnMin + readingGutter) + readingGap,
      rulesBeside: expanded && !empty && !hasReading,
    })};
}

type WindowRow = Readonly<{key: string; kind: 'section' | 'group' | 'entries'; estimate: number; label?: string; count?: number;
  metric?: boolean; reading?: boolean; ids?: readonly string[]; first?: boolean; last?: boolean}>;

/** One bounded native window inside the page's existing scroll owner. The same measured
 * geometry retains compact rounded groups and the tablet's two-column rows/rules split. */
export const DownloadInventoryWindow = React.memo(function DownloadInventoryWindow({inventory, membership, viewport, top, expanded, readingTablet,
  rulesBeside, paneWidth, device, runtime, copy, menuId, onMenu, onPlay, onRetry}: {
  inventory: DownloadInventory; membership: DownloadMembership; viewport: ReturnType<typeof createDownloadViewport>; top: number;
  expanded: boolean; readingTablet: boolean; rulesBeside: boolean; paneWidth: number;
  device: NativeOfflineDownloadsDevice; runtime: NativeShellRuntime; copy: NativeDownloadsCopy; menuId?: string;
  onMenu: (entry: OfflineEntry, anchor: RefObject<View | null>) => void; onPlay?: (entry: OfflineEntry) => void; onRetry: (entry: OfflineEntry) => void;
}) {
  const theme = useTheme(), visible = useSyncExternalStore(viewport.subscribe, viewport.getSnapshot);
  const readingColumn = (paneWidth - 2 * readingGutter - readingGap) / 2;
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
          <Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{row.label}</Text>
          {row.count !== undefined && <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{t('count.episodes', {count: row.count})}</Text>}
        </View> : <View style={{height: 8}}/> : <View style={expanded ? row.metric
          ? {flexDirection: 'row', paddingHorizontal: readingGutter, columnGap: readingGap}
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
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{label}</Text>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{defaultI18n.t('count.titles', {count})}</Text>
  </View>;
}

function DownloadsStorageKey({color, label, size, metric = false}: {color: string; label: string; size: number; metric?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: metric ? 7 : 6}}>
    <View style={{width: metric ? 9 : 7, height: metric ? 9 : 7, borderRadius: metric ? 2 : 4, backgroundColor: color}}/>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{label}</Text>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{defaultI18n.bytes(size)}</Text>
  </View>;
}

export function DownloadsRules({settings, pending, copy, save, onLimit, limitRef}: {settings: OfflineSettings; pending: boolean; copy: NativeDownloadsCopy; save: (settings: Partial<OfflineSettings>) => void; onLimit: () => void; limitRef: RefObject<View | null>}) {
  const theme = useTheme();
  const rows = [
    {field: 'wifiOnly', label: 'downloads.wifiOnly', help: 'downloads.wifiOnlyHelp'},
    {field: 'deleteWatched', label: 'downloads.removeAfterWatching', help: 'downloads.removeAfterWatchingHelp'},
  ] as const;
  const label = (title: string, help: string) => <View style={{flex: 1, minWidth: 0}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{help}</Text></View>;
  return <>
    <Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textTertiary, marginTop: 26, marginHorizontal: 20, marginBottom: 8}]}>{t('downloads.rules')}</Text>
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
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 2}}><Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{settings.limitBytes ? defaultI18n.bytes(settings.limitBytes) : t('downloads.noLimit')}</Text><Icon name="i-right" size={18} color={theme.colors.lineStrong}/></View>
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
      <View style={{flex: 1, minWidth: 0}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{entry.title}</Text>
        {entry.state === 'failed' && percent !== undefined && <Text style={[theme.type.captionStrong, {color: theme.colors.record, marginTop: 2}]}>{t(copy.stoppedAt, {percent})}</Text>}
      </View>
    </View>
    {entry.state === 'failed' && percent !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 10, marginHorizontal: 20, marginBottom: 2}]}>{t(copy.stoppedHelp, {percent})}</Text>}
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
          <View style={{flex: 1, minWidth: 0}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{entry.title}</Text>
            {!!text.metadata.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', columnGap: 10, rowGap: 2, marginTop: metric ? 3 : 2}}>
              {text.metadata.map((value, index) => <Text key={index} style={[theme.type.rowHeading, {maxWidth: '100%', color: theme.colors.textTertiary}]}>{value}</Text>)}
            </View>}
            {!!text.reason && <Text style={[theme.type.rowHelp, {color: text.stopped ? theme.colors.record : text.warning ? theme.colors.amber : theme.colors.textTertiary, marginTop: 2}]}>{text.reason}</Text>}
            {entry.state === 'downloading' && !entry.waiting && progress !== undefined && <><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{t(copy.downloading, {percent: Math.round(100 * progress)})}</Text><View style={{height: 3, backgroundColor: theme.colors.raisedBright, marginTop: metric ? 8 : 7, borderRadius: 2, overflow: 'hidden'}}><View style={{height: 3, width: `${100 * progress}%`, backgroundColor: theme.colors.accentStrong}}/></View></>}
            {text.stopped && <View style={{height: 37}}/>}
          </View>
        </>}
      </CardFrame>
      {text.stopped && <Chip testID={`download-retry-${encodeURIComponent(entry.id)}`} icon="i-retry" iconSize={14} onPress={onRetry} style={{position: 'absolute', bottom: 0, left: 59, height: 30, paddingHorizontal: 11}} textStyle={theme.type.captionStrong}>{t('action.tryAgain')}</Chip>}
    </View>
    <Focusable testID={`download-action-${encodeURIComponent(entry.id)}`} accessibilityLabel={t(playable ? (entry.kind === 'ebook' || entry.kind === 'comic' ? 'next.cards.read' : 'action.play') : 'card.more', {title: entry.title})}
      disabled={playable && !onPlay} onPress={playable ? onPlay : menu} style={{width: metric ? 44 : 34, height: metric ? 44 : 34, borderRadius: metric ? 22 : 17, alignItems: 'center', justifyContent: 'center', backgroundColor: playable ? theme.colors.text : undefined}}>
      <Icon name={playable ? (entry.kind === 'ebook' || entry.kind === 'comic' ? 'i-book' : 'i-play') : 'i-more'} size={playable ? 16 : 20} color={playable ? theme.colors.ink : theme.colors.textTertiary}/>
    </Focusable>
  </View>;
}

export function DownloadsEmpty({copy}: {copy: NativeDownloadsCopy}) {
  const theme = useTheme();
  return <View style={{paddingTop: 58, paddingHorizontal: 24, alignItems: 'center'}}>
          <View style={{width: 54, height: 54, borderRadius: 27, backgroundColor: alpha(theme.colors.text, .08), alignItems: 'center', justifyContent: 'center', marginBottom: 18}}><Icon name="i-dl" size={26} color={theme.colors.text}/></View>
          <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, textAlign: 'center', marginBottom: 10}]}>{t(theme.form === 'medium' || theme.form === 'expanded' ? 'next.saved.tablet.empty' : 'next.states.native.noDownloads')}</Text>
          <Text style={[theme.type.intro, {color: theme.colors.textSecondary, textAlign: 'center', marginBottom: 16}]}>{t(copy.emptyBody)}</Text>
        </View>;
}

export function DownloadStorage({snapshot, copy, expanded, readingTablet, heldBytes, readingBytes, watchingBytes, storage}: {
  snapshot: OfflineSnapshot; copy: NativeDownloadsCopy; expanded: boolean; readingTablet: boolean; storage: ReturnType<typeof import('../../screens/downloads/presentation').downloadStorageSegments>;
  heldBytes: number; readingBytes: number; watchingBytes: number;
}) {
  const theme = useTheme();
  const [barWidth, setBarWidth] = useState(0);
  const readingBarGap = readingTablet && watchingBytes > 0 && readingBytes > 0 ? 2 : 0;
  const availableWidth = Math.max(0, barWidth - readingBarGap);
  return <View style={readingTablet ? {marginHorizontal: 32, marginTop: 22, paddingVertical: 16, paddingHorizontal: 20, borderRadius: 16, backgroundColor: theme.colors.raised}
          : {marginHorizontal: 16, marginTop: 4, padding: 14, borderRadius: 14, backgroundColor: theme.colors.raised}}>
          <View style={{flexDirection: 'row', gap: 12, alignItems: 'baseline'}}>
          <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{t('downloads.used', {size: defaultI18n.bytes(snapshot.usedBytes)})}</Text>
          <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t(expanded ? 'downloads.freeOnDevice' : copy.freeOnPhone, {size: defaultI18n.bytes(snapshot.freeBytes)})}</Text>
          </View>
          {expanded ? (heldBytes > 0 && <>
            <View testID="downloads-storage-split" onLayout={readingTablet ? event => setBarWidth(event.nativeEvent.layout.width) : undefined}
              style={{height: readingTablet ? 7 : 6, borderRadius: readingTablet ? 4 : 3, backgroundColor: theme.colors.raisedBright,
              marginTop: readingTablet ? 12 : 10, marginBottom: readingTablet ? 10 : 0, overflow: 'hidden', flexDirection: 'row', gap: readingBarGap}}>
              <View style={{height: readingTablet ? 7 : 6, width: readingTablet ? availableWidth * watchingBytes / heldBytes : `${100 * watchingBytes / heldBytes}%`, backgroundColor: theme.colors.accentStrong}}/>
              <View style={{height: readingTablet ? 7 : 6, width: readingTablet ? availableWidth * readingBytes / heldBytes : `${100 * readingBytes / heldBytes}%`, backgroundColor: theme.colors.amber}}/>
            </View>
            <View style={{flexDirection: 'row', gap: readingTablet ? 20 : 18, marginTop: readingTablet ? 0 : 10, flexWrap: 'wrap'}}>
              <DownloadsStorageKey metric={readingTablet} color={theme.colors.accentStrong} label={t('next.live.watching')} size={watchingBytes}/>
              <DownloadsStorageKey metric={readingTablet} color={theme.colors.amber} label={t('next.settingsSpec.account.reading.title')} size={readingBytes}/>
            </View>
          </>) : storage && <View testID="downloads-storage-bar" style={{height: 6, borderRadius: 3, backgroundColor: theme.colors.raisedBright, marginTop: 10, overflow: 'hidden', flexDirection: 'row'}}>
            <View style={{height: 6, width: `${100 * storage.downloads}%`, backgroundColor: theme.colors.accentStrong}}/>
            <View style={{height: 6, width: `${100 * storage.other}%`, backgroundColor: alpha(theme.colors.accentStrong, .4)}}/>
          </View>}
        </View>;
}

export function DownloadsTransfers({requests, waiting, pending, cancel}: {requests: readonly TrackedDownloadRequest[]; waiting: NonNullable<OfflineSnapshot['pendingDownloads']>; pending: boolean; cancel?: (id: string) => void}) {
  const theme = useTheme();
  const requestText = (tracked: TrackedDownloadRequest): readonly string[] => downloadRequestLines(tracked.request).map(line =>
    line.key === 'downloads.requestProgress' ? t(line.key, line.values as {ready: number; total: number})
      : line.key === 'downloads.requestFailed' ? t(line.key, line.values as {failed: number}) : t(line.key));
  return <>
        {requests.length > 0 && <View testID="downloads-requests" style={{marginTop: 18, marginHorizontal: 16}}>
          <Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary, marginBottom: 8}]}>{t('downloads.requestsTitle')}</Text>
          <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
            {requests.map((tracked, index) => <View key={tracked.request.requestId} testID={`downloads-request-${tracked.request.requestId}`}
              style={{paddingHorizontal: 14, paddingVertical: 12, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .45)}}>
              <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{tracked.title}</Text>
              <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: 10, marginTop: 3}}>{requestText(tracked).map((text, index) =>
                <Text key={index} style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{text}</Text>)}</View>
            </View>)}
          </View>
        </View>}
        {waiting.length > 0 && <View testID="downloads-waiting" style={{marginTop: 18, marginHorizontal: 16, backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
          {waiting.map((entry, index) => <View key={entry.id} testID={`downloads-waiting-${entry.id}`}
            style={{paddingHorizontal: 14, paddingVertical: 12, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .45), flexDirection: 'row', alignItems: 'center', gap: 12}}>
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{entry.title}</Text>
              {!!entry.subtitle && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 3}]}>{entry.subtitle}</Text>}
              <Text style={[theme.type.rowHelp, {color: theme.colors.amber, marginTop: 3}]}>{entry.reason}</Text>
            </View>
            {cancel && <Chip testID={`downloads-cancel-${entry.id}`} disabled={pending} onPress={() => cancel(entry.id)}>{t('action.cancel')}</Chip>}
          </View>)}
        </View>}
  </>;
}

export function OtherProfileDownloads({size, pending, openRemoveOthers, copy}: {size: number; pending: boolean; openRemoveOthers: () => void; copy: NativeDownloadsCopy}) {
  const theme = useTheme();
  return <View style={{marginHorizontal: 16, marginTop: 14, paddingLeft: 14, paddingRight: 12, paddingVertical: 12, borderRadius: 12, borderWidth: 1, borderColor: alpha(theme.colors.text, .08), flexDirection: 'row', gap: 10, alignItems: 'center'}}>
    <Icon name="i-info" size={18} color={theme.colors.textSecondary}/><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, flex: 1}]}>{t(copy.otherProfiles, {size: defaultI18n.bytes(size)})}</Text>
    <Chip testID="downloads-remove-other-profiles" disabled={pending || size <= 0} onPress={openRemoveOthers}>{t('action.remove')}</Chip>
  </View>;
}

export function DownloadsScroll({node, inline, onWidth, viewport, children, overlays}: {node: RefObject<ScrollView | null>; inline?: boolean; onWidth: (width: number) => void; viewport: ReturnType<typeof createDownloadViewport>; children: ReactNode; overlays: ReactNode}) {
  return <View testID="native-downloads-screen" onLayout={event => onWidth(event.nativeEvent.layout.width)} style={{flex: 1}}>
    <ScrollView ref={node} style={{flex: 1}} scrollEventThrottle={64}
      onLayout={event => viewport.update({...viewport.getSnapshot(), height: event.nativeEvent.layout.height})}
      onScroll={event => viewport.update({...viewport.getSnapshot(), offset: event.nativeEvent.contentOffset.y})} contentContainerStyle={{paddingTop: inline ? 8 : 0, paddingBottom: 28}}>
      {children}
    </ScrollView>
    {overlays}
  </View>;
}

export function DownloadsInventory({beside, expanded, onTop, children, rules}: {beside: boolean; expanded: boolean; onTop: (top: number) => void; children?: ReactNode; rules: ReactNode}) {
  const theme = useTheme();
  return <View onLayout={event => onTop(event.nativeEvent.layout.y)} style={beside ? {flexDirection: 'row', alignItems: 'flex-start'} : undefined}>
    {children}
    <View style={beside ? {width: 360} : expanded ? {marginTop: theme.space.controlGap} : undefined}>{rules}</View>
  </View>;
}

export function DownloadEntryMenu({sheetMenu, menu, menuItems, anchor, close, afterMenu, copy}: {sheetMenu: boolean; menu?: OfflineEntry; menuItems: MenuAction[]; anchor?: RefObject<View | null>; close: () => void; afterMenu: () => void; copy: NativeDownloadsCopy}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), firstMenuAction = useRef<View>(null);
  return sheetMenu ? <BottomSheet testID="downloads-entry-menu" title={undefined} open={!!menu} onOpenChange={open => {if (!open) close();}}
      onExited={afterMenu}
      initialFocusRef={firstMenuAction} returnFocusRef={anchor} accessibilityLabel={menu?.title} headerContent={menu && <DownloadMenuContext entry={menu} copy={copy}/>}
      showClose={false} fitContent scrollable={false}>
      <View style={{paddingBottom: Math.max(26, insets.bottom)}}>{menuItems.map((action, index) => <Focusable key={action.id} ref={index === 0 ? firstMenuAction : undefined} testID={`downloads-entry-menu-item-${action.id}`}
        onPress={() => {close(); action.onSelect();}} style={{height: 54, paddingHorizontal: 20, flexDirection: 'row', alignItems: 'center', gap: 14}}>
        {action.icon && <Icon name={action.icon} size={22} color={action.destructive ? theme.colors.record : theme.colors.textSecondary}/>}
        <Text style={[theme.type.rowLabel, {color: action.destructive ? theme.colors.record : theme.colors.text}]}>{action.label}</Text>
      </Focusable>)}</View>
    </BottomSheet> : <Menu testID="downloads-entry-menu" title={undefined} open={!!menu} onOpenChange={open => {if (!open) close();}} anchorRef={anchor} returnFocusRef={anchor} accessibilityLabel={menu?.title}
      context={menu && <DownloadMenuContext entry={menu} copy={copy}/>} items={menuItems} presentation={theme.isTV ? 'sideSheet' : 'anchored'}/>;
}
