import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {useWindowDimensions, type ScrollView, type View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {DownloadsService, TrackedDownloadRequest} from '../../../../../../packages/client-core/src/downloads-service';
import type {NativeOfflineDownloadsDevice, OfflineEntry, OfflineSettings, OfflineSnapshot} from '../../../platform/offline-downloads';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {MenuAction} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import {downloadEntryIdentity, downloadGroups, downloadStorageSegments, type NativeDownloadsCopy} from '../../../screens/downloads/presentation';
import {createDownloadInventory, createDownloadViewport} from '../../../screens/downloads/inventory';

const t = defaultI18n.t;
const emptyRequests: readonly TrackedDownloadRequest[] = Object.freeze([]);
const noRequests = (): readonly TrackedDownloadRequest[] => emptyRequests;
const noSubscribe = (_listener: () => void) => () => {};
export const limits = [0, 5, 10, 25, 50, 100].map(gb => gb * 1073741824);


type OthersOpening = Readonly<{device: NativeOfflineDownloadsDevice; revision: number; bytes: number}>;
type RenderedOthers = Readonly<{device: NativeOfflineDownloadsDevice; snapshot: OfflineSnapshot; pending: boolean}>;

export type DownloadsPageProps = Readonly<{
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
export function useDownloads({device, runtime, copy, openTitle, service}: DownloadsPageProps) {
  const theme = useTheme(), dimensions = useWindowDimensions();
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
  // 09-downloads rule 30: removing a download asks first, then deletes it from the device at once.
  const [removing, setRemoving] = useState<OfflineEntry>(), removeAfterMenu = useRef<OfflineEntry | undefined>(undefined);
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
  const sheetMenu = dimensions.width < 600 && !theme.isTV;
  const menuItems: MenuAction[] = menu ? [
    ...(menu.state !== 'ready' ? [{id: 'resume', label: t('action.tryAgain'), icon: 'i-retry' as const, onSelect: () => run(() => device.resume(menu))}] : []),
    {id: 'title', label: t('web.downloads.goToTitle'), icon: 'i-film', onSelect: () => openTitle(menu)},
    {id: 'remove', label: t(copy.removeFromPhone), icon: 'i-trash', destructive: true, onSelect: () => {
      // The phone's menu is a sheet: the confirm opens once it has left, as one sheet may not open while another closes.
      if (sheetMenu) removeAfterMenu.current = menu; else setRemoving(menu);
      setEntryMenu(undefined);
    }},
  ] : [];
  const openMenu = useCallback((entry: OfflineEntry, anchor: RefObject<View | null>) => setEntryMenu({entry, anchor}), []);
  const playEntry = useCallback((entry: OfflineEntry) => {if (device.openOffline) run(() => device.openOffline!(entry));}, [device, run]);
  const retryEntry = useCallback((entry: OfflineEntry) => run(() => device.resume(entry)), [device, run]);
  const cancelWaiting = device.cancelPending ? (id: string) => run(() => device.cancelPending!(id)) : undefined;
  const afterMenu = () => {
    const entry = removeAfterMenu.current;
    removeAfterMenu.current = undefined;
    if (entry) setRemoving(entry);
  };
  const removeEntry = () => {
    const entry = removing;
    setRemoving(undefined);
    if (entry) run(() => device.remove(entry));
  };
  return {snapshot, inventory, membership, viewport, scroll, limitRef, inventoryTop, setInventoryTop, setPaneWidth, paneWidth,
    empty, storage, requests, waiting, cancelWaiting, expanded, readingTablet, readingColumn, rulesBeside, heldBytes,
    readingBytes, watchingBytes, readingBarWatching, readingBarReading, readingBarGap,
    failure, setEntryMenu, entryMenu, menu, menuItems, sheetMenu, afterMenu, pickingLimit, setPickingLimit,
    pending, removingOthers, closeRemoveOthers, openRemoveOthers, confirmRemoveOthers,
    removing, setRemoving, removeEntry, openMenu, playEntry, retryEntry, save, retry: () => run(device.open)};
}

function readingEntry(entry: OfflineEntry): boolean {
  const kind = entry.info?.kind ?? entry.kind; return kind === 'ebook' || kind === 'comic';
}
export type DownloadMembership = Readonly<{groups: ReturnType<typeof downloadGroups>; reading: readonly OfflineEntry[]; watching: ReturnType<typeof downloadGroups>}>;
export type DownloadInventory = ReturnType<typeof createDownloadInventory<OfflineEntry, DownloadMembership>>;
