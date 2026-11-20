import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import type {ReadingItem, ReadingManifest} from '../../../../../../packages/client-core/src';
import {readingResumePage} from '../../../../../../packages/client-core/src/reading';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AboutValue, StateRowProps, TitleAction, TitleKicker} from '../../../kit/frames';
import type {PosterRowProps} from '../../../kit/layout';
import type {TrackRowData} from '../../../kit/browse';
import type {MenuEntry} from '../../../kit/overlays';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {NativeShellRoute} from '../../../shell/routes';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {nativePublicationReadsAvailable} from '../../../../platform/offline-reading';
import type {NativeReadBook, NativeReadOwnerEditLease} from '../../../screens/read/read-book-model';
import type {ReadingActionsPort} from '../../../screens/read/ReadingActions';
import type {NativeReadingCollectionKind} from '../../../screens/read/read-collection-capability';
import {useReadingCollectionKind} from '../../../screens/read/use-reading-collection-kind';
import {askNativeDownload} from '../../../screens/downloads/request';

const {t} = defaultI18n;
const duration = (seconds: number) => durationText(seconds, t)!;
// The byline's circle: the first letters of the author's first two names (the old screen's rule).
const initials = (author: string): string => author.trim().split(/\s+/).slice(0, 2).map(part => part.slice(0, 1).toUpperCase()).join('') || '·';
const seriesNumber = (number: number, comic: boolean): string =>
  t(comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(number, {maximumFractionDigits: 20})});

type BookRoute = Extract<NativeShellRoute, {kind: 'read-book'}>;

/** An owner's sheet the host draws over the page: Edit details, or Add to collection for the kind the item admits. */
export type ReadBookSheet = Readonly<{kind: 'edit'; lease: NativeReadOwnerEditLease; manifest: ReadingManifest}
  | {kind: 'collection'; lease: NativeReadOwnerEditLease; collection: NativeReadingCollectionKind; title: string}>;

export type ReadBookPageProps = Readonly<{
  controller: NativeReadBook; route: BookRoute; comic: boolean; owner: boolean; context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime; onBack: () => void; onOpenSettings: () => void;
  /** The reading selection and item menu the host owns (the series shelf's cards select and open their menu through it). */
  readingActions: ReadingActionsPort;
  /** Opens an owner's sheet, which the host draws. */
  onSheet: (sheet: ReadBookSheet) => void;
}>;

/** What a book's page shows on a phone (phone-read 7, 8, 9), from the reading controller the old book screen read: data
 * only. Everything the old screen did is here: the versions, the saved place, My List or Read, Download, the ⋯ menu
 * (Mark read, Edit details, Add to collection, Details, Settings), the first lines, Contents and Details, and the rest of
 * the series. */
export type ReadBookView = Readonly<{
  id: string; title: string;
  cover?: ArtworkSource; glow?: AmbientArtSource; author?: string;
  kicker?: TitleKicker; byline?: Readonly<{text: string; initials: string}>;
  facts: readonly FactPart[];
  /** Read, or Continue reading with the saved place's progress in the button. */
  primary: Readonly<{label: string; progress?: number; disabled: boolean; onPress: () => void}>;
  actions: readonly TitleAction[];
  /** The version being read (a pill when there is more than one) and where the reader is. */
  place?: Readonly<{version?: Readonly<{label: string; value: string; onPress: () => void}>; facts?: readonly string[]}>;
  synopsis?: string; readMore?: Readonly<{label: string; expanded: boolean; onPress: () => void}>; firstLines?: string;
  state?: StateRowProps;
  /** Contents (an EPUB's chapters, the one being read marked) and Details; a book with no contents shows Details only. */
  tabs?: Readonly<{value: string; onValueChange: (value: string) => void}>;
  tab: 'contents' | 'details';
  contents: Readonly<{rows: readonly TrackRowData[]; onPlay: (id: string) => void}>;
  details: readonly Readonly<{label: string; values: readonly AboutValue[]}>[];
  shelf?: PosterRowProps; shelfState?: StateRowProps;
  menu: Readonly<{open: boolean; onOpenChange: (open: boolean) => void; items: readonly MenuEntry[]}>;
  versions: Readonly<{open: boolean; onOpenChange: (open: boolean) => void; items: readonly MenuEntry[]}>;
  more: () => void;
}>;

export function useReadBook({controller, route, comic, owner, context, runtime, onOpenSettings, readingActions, onSheet}: ReadBookPageProps): ReadBookView {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const shelf = useSyncExternalStore(controller.subscribeShelf, controller.shelfSnapshot);
  const [menuOpen, setMenuOpen] = useState(false), [versionsOpen, setVersionsOpen] = useState(false), [expanded, setExpanded] = useState(false), [tab, setTab] = useState<'contents' | 'details'>('contents');
  const downloadAnchor = useRef<View | null>(null);
  useLayoutEffect(() => {
    if (!controller.arm()) return;
    void controller.load().catch(() => {});
    return () => controller.release();
  }, [controller, route]);
  const run = (work: Promise<unknown>) => {void work.catch(runtime.reportError);};
  const ready = state.phase === 'ready' && !!state.manifest, manifest = ready ? state.manifest : undefined;
  const series = controller.series(), saved = manifest ? controller.savedProgression(manifest) : undefined;
  // The rest of this series, from the same series read the series page uses, once this page's own manifest is ready.
  useEffect(() => {if (manifest && series) void controller.loadSeriesShelf(manifest, series).catch(() => {});}, [controller, manifest, series]);
  // The sheet lease is taken from this committed ready render; a menu kept after Back cannot borrow the next one's.
  const editLease = manifest ? controller.ownerEditLease(manifest) : undefined;
  const collection = useReadingCollectionKind({context, controller, libraryId: route.libraryId, manifest, editLease});
  const item = controller.item, title = manifest?.metadata.title || item.title, author = manifest?.metadata.author || item.author?.value;
  const art = item.coverUrl ?? manifest?.metadata.coverUrl;
  const contents = manifest?.format === 'epub' ? manifest.contents ?? [] : [];
  const pages = manifest ? manifest.metadata.pageCount ?? manifest.pageNames?.length : undefined;
  const estimated = manifest?.metadata.estimatedSeconds;
  // The saved place, only when it is this version's and not stale (the old screen's rule for the marked chapter).
  const progress = state.progress?.progress;
  const place = manifest && progress && !progress.stale && progress.sourceId === manifest.sourceId && progress.sourceRevision === manifest.revision ? progress : undefined;
  const section = progress && !progress.stale ? progress.location.section : undefined;
  const currentIndex = section === undefined ? -1 : contents.findIndex(entry => entry.section === section);
  const read = (index?: number) => {if (manifest) run(controller.read(manifest, index));};
  const openSeries = series && manifest ? () => run(controller.openSeries(series, manifest)) : undefined;
  const canDownload = nativePublicationReadsAvailable();
  const facts: FactPart[] = manifest ? [
    ...(manifest.metadata.wordCount !== undefined ? [{id: 'words', text: t('next.reader.wordCount', {count: manifest.metadata.wordCount}), role: 'context' as const}] : []),
    ...(estimated !== undefined ? [{id: 'duration', text: t('next.reader.aboutDuration', {duration: duration(estimated)}), role: 'context' as const}] : []),
    ...(pages !== undefined ? [{id: 'pages', text: t('next.reader.pageCount', {count: pages}), role: 'context' as const}] : []),
    ...(contents.length ? [{id: 'chapters', text: t('next.kit.chapters', {count: contents.length}), role: 'context' as const}] : []),
  ] : [];
  // Where the reader is (phone-read 7 "Page 248 of 535  About 7 h left"): a page book's page, an EPUB's chapter by its
  // title, then the time left from the book's own estimate. Nothing is shown for a place the server did not save.
  const page = place ? readingResumePage(place) : undefined;
  const placeFacts = place && saved !== undefined ? [
    ...(page !== undefined && pages !== undefined ? [t('next.reader.pageOf', {page, count: pages})] : currentIndex >= 0 ? [contents[currentIndex]!.title] : []),
    ...(estimated !== undefined ? [t('next.reader.aboutLeft', {duration: duration(Math.max(60, estimated * (1 - saved)))})] : []),
  ] : [];
  const selected = state.sourceId ? state.versions.find(version => version.sourceId === state.sourceId) : undefined;
  const failed = state.phase === 'error' ? presentError(state.error, 'detail', {operation: 'load'}) : undefined;
  const shelfFailed = shelf.phase === 'error' || shelf.moreError ? presentError(shelf.phase === 'error' ? shelf.error : shelf.moreError, 'library', {operation: 'load'}) : undefined;
  const shelfOffer = (value: ReadingItem) => {
    const lifetime = manifest ? controller.shelfItemLifetime(shelf.witness, manifest, value) : undefined;
    return manifest && lifetime ? {item: value, lifetime, open: () => run(controller.openSeriesItem(shelf.witness, manifest, value)),
      ...(canDownload ? {download: () => askNativeDownload({title: value.title, itemId: value.id, reading: true, lifetime})} : {})} : undefined;
  };
  const close = (then: () => void) => () => {setMenuOpen(false); then();};
  return {
    id: item.id, title, author, cover: runtime.artwork(art), glow: runtime.ambient(art),
    // phone-read 7 "Dune, book 1"; a comic names its volume.
    kicker: series ? {label: series.number === undefined ? series.name : comic ? series.name : t('title.seriesAndBook', {series: series.name, number: defaultI18n.number(series.number, {maximumFractionDigits: 20})}),
      detail: comic && series.number !== undefined ? seriesNumber(series.number, true) : undefined, onPress: openSeries} : undefined,
    byline: author ? {text: author, initials: initials(author)} : undefined,
    facts,
    primary: {label: t(saved === undefined ? 'next.reader.read' : 'next.cards.read'), progress: saved, disabled: !manifest, onPress: () => read()},
    actions: manifest ? [
      // An EPUB is put on My List; a page book (PDF, comic) is marked read in its place, as the old page did.
      ...(manifest.format === 'epub' && state.listed ? [{icon: state.listed.listed ? 'i-check' as const : 'i-plus' as const, label: t(state.listed.listed ? 'next.home.removeMyList' : 'next.home.addMyList'),
        active: state.listed.listed, onPress: () => run(controller.toggleList(state.listed!)), testID: 'read-book-list'}] : []),
      ...(manifest.format !== 'epub' && state.read ? [{icon: 'i-check' as const, label: t(state.read.read ? 'next.reader.markUnread' : 'next.reader.markRead'),
        active: state.read.read, onPress: () => run(controller.toggleRead(state.read!)), testID: 'read-book-mark'}] : []),
      {icon: 'i-dl', label: t('action.download'), disabled: !canDownload, onPress: () => {controller.download(manifest, downloadAnchor);}, testID: 'read-book-download'},
    ] : [],
    place: manifest && (state.versions.length > 1 || placeFacts.length) ? {
      version: state.versions.length > 1 ? {label: t('next.reader.version'), value: selected?.format.toUpperCase() ?? '', onPress: () => setVersionsOpen(true)} : undefined,
      facts: placeFacts.length ? placeFacts : undefined} : undefined,
    synopsis: manifest?.metadata.description, firstLines: manifest?.metadata.openingLines,
    // The description opens in place with Read more and closes with Show less (as a book's and a person's page).
    readMore: manifest?.metadata.description ? {label: t(expanded ? 'web.person.showLess' : 'web.person.readMore'), expanded, onPress: () => setExpanded(value => !value)} : undefined,
    state: failed ? {kind: 'error', icon: 'i-warn', title: failed.title, action: {label: failed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.retry())}}
      : !ready ? {kind: 'loading'} : undefined,
    tabs: contents.length ? {value: tab, onValueChange: value => setTab(value === 'details' ? 'details' : 'contents')} : undefined,
    tab: contents.length ? tab : 'details',
    // The contents DTO carries no length or page, so a row is its place and its title; the one being read is marked.
    contents: {rows: contents.map((entry, index) => ({id: String(index), number: index + 1, text: {id: String(index), title: entry.title, caption: [], detail: []},
      current: index === currentIndex})), onPlay: id => read(Number(id))},
    details: manifest ? [
      ...(series && openSeries ? [{label: t('library.tab.series'), values: [{text: series.number === undefined ? series.name : `${series.name}, ${seriesNumber(series.number, comic)}`, onPress: openSeries}]}] : []),
      ...(author ? [{label: t('filter.field.author'), values: [{text: author}]}] : []),
      {label: t('next.reader.format'), values: [{text: manifest.format.toUpperCase()}]},
      {label: t('next.reader.fileSize'), values: [{text: defaultI18n.bytes(manifest.size)}]},
      ...(manifest.metadata.language ? [{label: t('next.selection.language'), values: [{text: manifest.metadata.language}]}] : []),
      ...(pages !== undefined ? [{label: t('next.reader.pagesStrip'), values: [{text: defaultI18n.number(pages)}]}] : []),
      ...(contents.length ? [{label: t('next.reader.contents'), values: [{text: defaultI18n.number(contents.length)}]}] : []),
    ] : [],
    // The rest of the series (tablet-read 2b, phone-read 8's row): its volumes in order, paging on, each opening, selecting
    // and opening its menu through the reading actions; a volume the server can't serve now is drawn but takes nothing.
    shelf: series && manifest && shelf.items.length ? {title: t('next.reader.restOfSeries'), subtitle: t('next.reader.seriesInOrder', {series: series.name}),
      testID: 'native-read-book-series-shelf', onSeeAll: openSeries,
      items: shelf.items.map(value => {
        const offer = value.inventoryAvailable === true ? shelfOffer(value) : undefined;
        return {key: `${value.id}:${value.sourceId}`, title: value.title, artwork: runtime.artwork(value.coverUrl),
          unavailable: value.inventoryAvailable === true ? undefined : t('next.reader.volumeUnavailable'),
          caption: value.series?.number === undefined ? undefined : seriesNumber(value.series.number, comic),
          onNavigate: () => run(controller.openSeriesItem(shelf.witness, manifest, value)),
          selection: offer ? readingActions.selection(offer) : undefined,
          onMore: offer ? (anchorRef: RefObject<View | null>) => readingActions.actions.show({...offer, anchorRef}) : undefined};
      }),
      paging: {hasMore: !!shelf.nextCursor && !shelf.moreError, loading: shelf.morePending === true, failed: !!shelf.moreError,
        waitingId: `shelf:${shelf.nextCursor ?? 'end'}`, waitingItem: {key: `shelf:${shelf.nextCursor ?? 'end'}`, title: '', onNavigate: () => {}},
        requestNext: () => run(controller.moreSeriesShelf(shelf.witness, manifest))}} : undefined,
    shelfState: series && manifest && shelfFailed ? {kind: 'error', icon: 'i-warn', title: shelfFailed.title,
      action: {label: shelfFailed.actionLabel ?? t('action.tryAgain'), onPress: () => run(controller.retrySeriesShelf(shelf.witness, manifest))}} : undefined,
    menu: {open: menuOpen, onOpenChange: setMenuOpen, items: [
      ...(state.read ? [{id: 'read-mark', label: t(state.read.read ? 'next.reader.markUnread' : 'next.reader.markRead'), icon: 'i-check' as const,
        onSelect: close(() => run(controller.toggleRead(state.read!)))}] : []),
      ...(owner && editLease && manifest ? [{id: 'edit-details', label: t('next.reader.editDetails'), icon: 'i-edit' as const, tone: 'owner' as const,
        onSelect: () => {if (!editLease.isCurrent()) return; setMenuOpen(false); onSheet({kind: 'edit', lease: editLease, manifest});}}] : []),
      ...(editLease && collection ? [{id: 'add-to-collection', label: t('next.selection.addToCollection'), icon: 'i-layers' as const,
        onSelect: () => {if (!editLease.isCurrent()) return; setMenuOpen(false); onSheet({kind: 'collection', lease: editLease, collection, title});}}] : []),
      // Every row has its icon, so the rows line up (no row reads as a selected tick).
      ...(contents.length ? [{id: 'details', label: t('next.reader.details'), icon: 'i-info' as const, onSelect: close(() => setTab('details'))}] : []),
      {id: 'settings', label: t('next.reader.openSettings'), icon: 'i-settings' as const, onSelect: close(onOpenSettings)},
    ]},
    // The versions sheet: each real version with its size; one the server can't serve now can't be chosen.
    versions: {open: versionsOpen, onOpenChange: setVersionsOpen, items: manifest ? state.versions.map(version => ({
      id: version.sourceId, label: version.format.toUpperCase(), description: defaultI18n.bytes(version.size),
      selected: version.sourceId === state.sourceId, disabled: !version.inventoryAvailable,
      onSelect: () => {setVersionsOpen(false); if (version.inventoryAvailable && version.sourceId !== state.sourceId) run(controller.selectVersion(manifest, state.versions, version.sourceId));},
    })) : []},
    more: () => setMenuOpen(true),
  };
}
