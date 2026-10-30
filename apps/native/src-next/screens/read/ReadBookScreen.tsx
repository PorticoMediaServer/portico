import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingItem, ReadingListMembership, ReadingManifest, ReadingReadMark} from '../../../../../packages/client-core/src';
import {Artwork, AmbientArt, GeneratedCover, StandardBackdrop} from '../../kit/artwork';
import {Button, Focusable, ResumeButton, Tabs} from '../../kit/controls';
import {Icon, type IconName} from '../../kit/Icon';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {StateView, SkeletonBlock, LoadingPaint} from '../../kit/states';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import {shellLayout} from '../../shell/model';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeReadBook, NativeReadSeriesShelfState} from './read-book-model';
import type {NativeReadOwnerEditLease} from './read-book-model';
import {VolumeCard} from './ReadSeriesScreen';
import {ReadingShelf} from './ReadingShelf';
import {ReadingItemMenu, ReadingSelectionToolbar, useReadingActions, type ReadingActionsPort} from './ReadingActions';
import type {NativeReadingActionOffer} from './reading-actions';
import {askNativeDownload} from '../downloads/request';
import type {NativeReadVersion} from './read-book-model';
import {ReadOwnerEditSheet} from './ReadOwnerEditSheet';
import {ReadCollectionSheet} from './ReadCollectionSheet';
import {BookDetailActions} from './book-detail-actions';
import {nativeReadingCollectionCapability, type NativeReadingCollectionKind} from './read-collection-capability';
import {nativeScreenServices} from '../native-screen-services';
import {isReadingLibraryKind} from './platform';
import {linkNativeAbortSignals} from '../../platform/abort-signals';

const {t} = defaultI18n;
const initials = (author: string): string => author.trim().split(/\s+/).slice(0, 2).map(part => part.slice(0, 1).toUpperCase()).join('') || '·';
const seriesNumber = (number: number, comic: boolean): string =>
  t(comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(number, {maximumFractionDigits: 20})});
const ROW = {borderTopWidth: 1} as const;

type BookRoute = Extract<NativeShellRoute, {kind: 'read-book'}>;
type SeriesLink = Readonly<{id: string; name: string; number?: number}>;

/** phone-read 3/3c and tablet-read 2/2b/2c: the book's own page before the
 * reader. Real versions, manifest, saved position, declared contents and
 * descriptive facts only; choosing another real available version reloads this
 * page's projection for that source. The Read action is the one openNativeRead
 * door with the exact manifest it drew. */
export function ReadBookScreen({controller, route, comic, owner, context, runtime, onBack, onOpenSettings}: {
  controller: NativeReadBook; route: BookRoute; comic: boolean; owner: boolean; context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime; onBack: () => void; onOpenSettings: () => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const shelf = useSyncExternalStore(controller.subscribeShelf, controller.shelfSnapshot);
  const readingActions = useReadingActions(context, runtime, controller);
  // tablet-read 2/2b draw one reading column and one 380 pt contents column side by side;
  // 2c keeps the single column. The shell's own classification decides: a landscape phone
  // (wide but short, e.g. 914x411) stays a phone page, and portrait keeps one column.
  const layout = shellLayout(dimensions.width, dimensions.height, theme.isTV);
  const wide = dimensions.width > dimensions.height && layout.kind !== 'phone', rowHero = layout.kind !== 'phone';
  const [more, setMore] = useState(false), [tab, setTab] = useState('contents'), [heroHeight, setHeroHeight] = useState(0), [collapsed, setCollapsed] = useState(false);
  const [edit, setEdit] = useState<Readonly<{lease: NativeReadOwnerEditLease; owner: object; key: number}>>();
  const [collection, setCollection] = useState<Readonly<{lease: NativeReadOwnerEditLease; owner: object; key: number; kind: NativeReadingCollectionKind}>>();
  const [collectionCapability, setCollectionCapability] = useState<Readonly<{lease: NativeReadOwnerEditLease; manifest: ReadingManifest; kind?: NativeReadingCollectionKind}>>();
  const editSeq = useRef(0);
  useLayoutEffect(() => {
    if (!controller.arm()) return;
    void controller.load().catch(() => {});
    return () => controller.release();
  }, [controller, route]);
  const manifest = state.manifest, title = manifest?.metadata.title ?? controller.item.title;
  // The exact selected version from the page's versions producer, never looked up again.
  const selectedVersion = state.sourceId ? state.versions.find(value => value.sourceId === state.sourceId) : undefined;
  const series = controller.series(), saved = manifest ? controller.savedProgression(manifest) : undefined;
  // tablet-read 2b: the rest of this series, read from the same signed series
  // producer the series page uses, once this page's own manifest is ready.
  useEffect(() => {if (state.phase === 'ready' && manifest && series) void controller.loadSeriesShelf(manifest, series).catch(() => {});}, [controller, manifest, series, state.phase]);
  const savedProgress = state.progress?.progress;
  const currentSection = savedProgress && !savedProgress.stale ? savedProgress.location.section : undefined;
  const contents = manifest?.format === 'epub' ? manifest.contents ?? [] : [];
  // The sheet lease is taken from this committed ready render; a retained menu
  // after Back cannot borrow the successor arm/life.
  const editLease = manifest ? controller.ownerEditLease(manifest) : undefined;
  // The collection capability is asked of the exact item's Detail offer for
  // this committed page under a real Reading library, never from a format or a
  // library spelling; the producer's admitted ebook/comic kind is cached with
  // that original lease and manifest.
  const library = context.libraries().find(value => value.id === route.libraryId);
  const leaseSignal = editLease?.signal, leaseOrigin = editLease?.origin;
  useLayoutEffect(() => {
    const lease = editLease;
    if (!lease || !manifest || !library || !isReadingLibraryKind(library.kind) || !lease.isCurrent()) return;
    const life = new AbortController(), linked = linkNativeAbortSignals([lease.signal, life.signal]);
    const current = () => !linked.signal.aborted && lease.isCurrent() && context.current();
    const services = (() => {try {return nativeScreenServices(context);} catch {return undefined;}})();
    if (!services) return;
    const scope = {serverId: lease.viewer.serverId, viewerId: JSON.stringify([lease.viewer.authority, lease.viewer.accountId, lease.viewer.profileId])};
    void nativeReadingCollectionCapability({api: context.api, scope, requestId: services.requestId, signal: linked.signal,
      isCurrent: current, itemId: controller.item.id, libraryId: route.libraryId})
      .then(kind => {if (current()) setCollectionCapability({lease, manifest, kind});})
      .catch(() => {if (current()) setCollectionCapability({lease, manifest});});
    return () => {life.abort(); linked.dispose();};
  }, [context, controller, manifest, route.libraryId, library?.id, library?.kind, leaseSignal, leaseOrigin]); // eslint-disable-line react-hooks/exhaustive-deps
  const offeredKind = collectionCapability && editLease && library && isReadingLibraryKind(library.kind)
    && collectionCapability.manifest === manifest && collectionCapability.lease.signal === editLease.signal
    && collectionCapability.lease.isCurrent() ? collectionCapability.kind : undefined;
  const menu: readonly MenuEntry[] = [
    ...(state.read ? [{id: 'read-mark', label: t(state.read.read ? 'next.reader.markUnread' : 'next.reader.markRead'), icon: 'i-check' as IconName,
      onSelect: () => {setMore(false); toggleRead(state.read!);}}] : []),
    ...(owner && editLease ? [{id: 'edit-details', label: t('next.reader.editDetails'), tone: 'owner' as const,
      onSelect: () => {if (!editLease.isCurrent()) return; setMore(false); setEdit({lease: editLease, owner: {}, key: ++editSeq.current});}}] : []),
    ...(editLease && offeredKind ? [{id: 'add-to-collection', label: t('next.selection.addToCollection'), icon: 'i-layers' as IconName,
      onSelect: () => {if (!editLease.isCurrent()) return; setMore(false); setCollection({lease: editLease, owner: {}, key: ++editSeq.current, kind: offeredKind});}}] : []),
    ...(contents.length ? [{id: 'details', label: t('next.reader.details'), selected: tab === 'details', onSelect: () => {setTab('details'); setMore(false);}}] : []),
    {id: 'settings', label: t('next.reader.openSettings'), onSelect: () => {setMore(false); onOpenSettings();}},
  ];
  const read = (target: ReadingManifest, index?: number) => {
    void controller.read(target, index).then(ok => {if (!ok) return;}).catch(runtime.reportError);
  };
  const toggleList = (membership: ReadingListMembership) => {
    void controller.toggleList(membership).then(ok => {if (!ok) return;}).catch(runtime.reportError);
  };
  const toggleRead = (mark: ReadingReadMark) => {
    void controller.toggleRead(mark).then(ok => {if (!ok) return;}).catch(runtime.reportError);
  };
  const downloadAnchor = useRef<View | null>(null);
  const canDownload = nativePublicationReadsAvailable();
  const download = (manifest: ReadingManifest) => {controller.download(manifest, downloadAnchor);};
  const openSeries = (target: SeriesLink, manifest: ReadingManifest) => {
    void controller.openSeries(target, manifest).then(ok => {if (!ok) return;}).catch(runtime.reportError);
  };
  const selectVersion = (manifest: ReadingManifest, versions: readonly NativeReadVersion[], sourceId: string) => {
    void controller.selectVersion(manifest, versions, sourceId).catch(runtime.reportError);
  };
  const ready = state.phase === 'ready' && !!manifest;
  const body = ready && manifest
    ? <ReadBookBody controller={controller} manifest={manifest} series={series} comic={comic} runtime={runtime} wide={wide} rowHero={rowHero}
      saved={saved} currentSection={currentSection} tab={tab} onTab={setTab} onRead={read} onSeries={openSeries}
      versions={state.versions} sourceId={state.sourceId} onVersion={selectVersion}
      listed={state.listed} onList={toggleList} onMore={editLease ? () => {if (editLease.isCurrent()) setMore(true);} : undefined} readMark={state.read} onReadMark={toggleRead} canDownload={canDownload} onDownload={download} downloadRef={downloadAnchor} onHeroHeight={setHeroHeight}
      shelf={shelf} readingActions={readingActions} shelfOffer={item => {
        const lifetime = controller.shelfItemLifetime(shelf.witness, manifest, item);
        return lifetime ? {item, lifetime, open: () => {void controller.openSeriesItem(shelf.witness, manifest, item).catch(runtime.reportError);},
          ...(canDownload ? {download: () => askNativeDownload({title: item.title, itemId: item.id, reading: true, lifetime})} : {})} : undefined;
      }} onShelfMore={() => {void controller.moreSeriesShelf(shelf.witness, manifest).catch(runtime.reportError);}}
      onShelfRetry={() => {void controller.retrySeriesShelf(shelf.witness, manifest).catch(runtime.reportError);}}
      onShelfItem={item => {void controller.openSeriesItem(shelf.witness, manifest, item).then(ok => {if (!ok) return;}).catch(runtime.reportError);}}/>
    : null;
  return <View testID={`native-read-book-${controller.item.id}`} style={{flex: 1}}><StandardBackdrop/>
    <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 560, overflow: 'hidden'}}>
      {runtime.ambient(controller.item.coverUrl ?? manifest?.metadata.coverUrl) ? <AmbientArt source={runtime.ambient(controller.item.coverUrl ?? manifest?.metadata.coverUrl)} treatment="title" style={{position: 'absolute', top: 0, left: 0, right: 0, bottom: 0}}/> : null}
      <View style={{position: 'absolute', top: 0, left: 0, right: 0, bottom: 0, backgroundColor: theme.colors.ink, opacity: .5}}/>
    </View>
    {rowHero ? <>
      <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack}
        style={{position: 'absolute', top: insets.top + 6, left: 10, zIndex: 10, height: 44, width: 44}}/>
      <Button variant="icon" icon="i-more" iconOnly accessibilityLabel={t('next.controls.more')} onPress={() => setMore(true)}
        style={{position: 'absolute', top: insets.top + 6, right: 10, zIndex: 10, height: 44, width: 44}}/>
    </> : <>
      {/* phone-read 3/3c: past the hero the page pins its bar and the Contents/Details
          tabs, keeping the same back and More actions in place. */}
      <View pointerEvents={collapsed ? 'auto' : 'box-none'} style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10,
        paddingTop: insets.top + 6, height: insets.top + 50 + (collapsed && contents.length ? 42 : 0),
        backgroundColor: collapsed ? theme.colors.ink : 'transparent', borderBottomWidth: collapsed ? 1 : 0, borderBottomColor: theme.colors.line}}>
        <View pointerEvents="box-none" style={{height: 44, paddingHorizontal: 10, justifyContent: 'center'}}>
          <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack} style={{height: 44, width: 44}}/>
          {collapsed ? <View pointerEvents="none" style={{position: 'absolute', left: 64, right: 64, height: 44, justifyContent: 'center'}}>
            <Text numberOfLines={1} style={[browseType(theme, 17, 700), {textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
          </View> : null}
        </View>
        {collapsed && contents.length ? <Tabs testID="native-read-book-pin-tabs" value={tab} onValueChange={setTab} style={{height: 42}}
          items={[{value: 'contents', label: t('next.reader.contents')}, {value: 'details', label: t('next.reader.details')}]}/> : null}
      </View>
      <Button variant="icon" icon="i-more" iconOnly accessibilityLabel={t('next.controls.more')} onPress={() => setMore(true)}
        style={{position: 'absolute', top: insets.top + 6, right: 10, zIndex: 11, height: 44, width: 44}}/>
    </>}
    <ScrollView contentContainerStyle={{paddingTop: Math.max(96, insets.top + 56), paddingBottom: insets.bottom + 48}} showsVerticalScrollIndicator={false}
      scrollEventThrottle={64}
      onScroll={event => {const y = event.nativeEvent.contentOffset.y;
        setCollapsed(current => {const next = !rowHero && heroHeight > 0 && y > heroHeight - 50; return next === current ? current : next;});}}>
      {state.phase === 'error'
        ? <StateView {...runtime.error(state.error, () => {void controller.retry().catch(runtime.reportError);}, 'detail')}/>
        : <LoadingPaint ready={ready} immediate={ready} skeleton={<SkeletonBlock width="100%" height={520}/>}
          style={ready ? {flexGrow: 0, flexShrink: 0, flexBasis: 'auto'} : {height: 520}} testID="native-read-book-paint">
          {body}
        </LoadingPaint>}
    </ScrollView>
    <Menu open={more} onOpenChange={setMore} title={title} items={menu}/>
    <ReadingItemMenu port={readingActions} runtime={runtime}/>
    {edit ? <ReadOwnerEditSheet key={edit.key} context={context} lease={edit.lease} owner={edit.owner} itemId={controller.item.id} libraryId={route.libraryId} manifest={manifest} version={selectedVersion}
      onClose={ticket => setEdit(current => current?.owner === ticket ? undefined : current)}/> : null}
    {collection ? <ReadCollectionSheet key={collection.key} context={context} lease={collection.lease} owner={collection.owner} itemId={controller.item.id} libraryId={route.libraryId} kind={collection.kind} title={title}
      onClose={ticket => setCollection(current => current?.owner === ticket ? undefined : current)}/> : null}
  </View>;
}

function ReadBookBody({controller, manifest, series, comic, runtime, wide, rowHero, saved, currentSection, tab, onTab, onRead, onSeries, versions, sourceId, onVersion, listed, onList, onMore, readMark, onReadMark, canDownload, onDownload, downloadRef, onHeroHeight, shelf, readingActions, shelfOffer, onShelfMore, onShelfRetry, onShelfItem}: {
  controller: NativeReadBook; manifest: ReadingManifest; series?: SeriesLink; comic: boolean; runtime: NativeShellRuntime; wide: boolean; rowHero: boolean;
  saved?: number; currentSection?: string; tab: string; onTab: (value: string) => void; onRead: (manifest: ReadingManifest, index?: number) => void;
  onSeries: (series: SeriesLink, manifest: ReadingManifest) => void;
  versions: readonly NativeReadVersion[]; sourceId?: string;
  onVersion: (manifest: ReadingManifest, versions: readonly NativeReadVersion[], sourceId: string) => void;
  listed?: ReadingListMembership; onList: (membership: ReadingListMembership) => void; onMore?: () => void;
  readMark?: ReadingReadMark; onReadMark: (mark: ReadingReadMark) => void;
  canDownload: boolean; onDownload: (manifest: ReadingManifest) => void; downloadRef: React.RefObject<View | null>; onHeroHeight: (height: number) => void;
  shelf: NativeReadSeriesShelfState; onShelfMore: () => void; onShelfRetry: () => void; onShelfItem: (item: ReadingItem) => void;
  readingActions: ReadingActionsPort; shelfOffer: (item: ReadingItem) => NativeReadingActionOffer | undefined;
}) {
  const theme = useTheme(), [versionsOpen, setVersionsOpen] = useState(false), phone = theme.platform === 'phone';
  const selectedVersion = versions.find(version => version.sourceId === sourceId);
  const title = manifest.metadata.title || controller.item.title, author = manifest.metadata.author || controller.item.author?.value;
  const cover = runtime.artwork(controller.item.coverUrl ?? manifest.metadata.coverUrl), tint = generatedCoverTint(title);
  // tablet-read 2/2b draw the hero as a row (cover beside the copy) with an 84 pt
  // title and a 206 pt cover; 2c uses the med row (66/176); phone keeps the stack.
  // The shell's own classification decides, so a landscape phone is never a row.
  const coverWidth = rowHero ? (wide ? 206 : 176) : 168, coverHeight = coverWidth * 3 / 2;
  const titleSize = rowHero ? (wide ? 84 : 66) : 40;
  const duration = manifest.metadata.estimatedSeconds !== undefined ? defaultI18n.duration(manifest.metadata.estimatedSeconds) : '';
  const pages = manifest.metadata.pageCount ?? manifest.pageNames?.length;
  const contents = manifest.format === 'epub' ? manifest.contents ?? [] : [];
  const facts = [
    ...(manifest.metadata.wordCount !== undefined ? [t('next.reader.wordCount', {count: manifest.metadata.wordCount})] : []),
    ...(duration ? [duration] : []),
    ...(pages !== undefined ? [t('next.reader.pageCount', {count: pages})] : []),
    ...(contents.length ? [t('next.kit.chapters', {count: contents.length})] : []),
  ];
  const detailFacts = [
    ...(author ? [{label: t('filter.field.author'), value: author}] : []),
    {label: t('next.reader.format'), value: manifest.format.toUpperCase()},
    {label: t('next.reader.fileSize'), value: defaultI18n.bytes(manifest.size)},
    ...(manifest.metadata.language ? [{label: t('next.selection.language'), value: manifest.metadata.language}] : []),
    ...(pages !== undefined ? [{label: t('next.reader.pagesStrip'), value: defaultI18n.number(pages)}] : []),
    ...(contents.length ? [{label: t('next.reader.contents'), value: defaultI18n.number(contents.length)}] : []),
  ];
  const coverArt = <View style={{width: coverWidth, height: coverHeight, borderRadius: 7, overflow: 'hidden', backgroundColor: theme.colors.raised,
    shadowColor: theme.colors.shadow, shadowOpacity: .55, shadowRadius: 30, shadowOffset: {width: 0, height: 18}, elevation: 12}}>
    {cover ? <Artwork source={cover} width={coverWidth} height={coverHeight} priority/>
      : <GeneratedCover title={title} author={author} tint={tint} glyph={title.slice(0, 1).toUpperCase()}/>}
  </View>;
  const copy = <>
    {series ? <Focusable accessibilityRole="link" accessibilityLabel={series.name}
      onPress={() => onSeries(series, manifest)} style={{marginTop: rowHero ? 0 : 14, paddingVertical: 4, maxWidth: '100%'}}>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 4, justifyContent: rowHero ? 'flex-start' : 'center'}}>
        <Text numberOfLines={2} style={[browseType(theme, rowHero ? 14.5 : 13.5, 600), {color: theme.colors.accentStrong}]}>{series.name}</Text>
        {series.number !== undefined ? <Text style={[browseType(theme, rowHero ? 14.5 : 13.5, 600), {color: theme.colors.accentStrong}]}>{seriesNumber(series.number, comic)}</Text> : null}
      </View>
    </Focusable> : null}
    <Text accessibilityRole="header" numberOfLines={3} style={[browseType(theme, titleSize, 800, rowHero ? -.045 : -.03), {color: theme.colors.text, textAlign: rowHero ? 'left' : 'center', marginTop: 6}]}>{title}</Text>
    {author ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 7, marginTop: 8}}>
      <View style={{width: rowHero ? 28 : 22, height: rowHero ? 28 : 22, borderRadius: rowHero ? 14 : 11, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}>
        <Text style={[browseType(theme, rowHero ? 11 : 9.5, 800), {color: theme.colors.text}]}>{initials(author)}</Text>
      </View>
      <Text numberOfLines={1} style={[browseType(theme, rowHero ? (wide ? 14.5 : 14) : 13, 700), {color: theme.colors.text, flexShrink: 1}]}>{author}</Text>
    </View> : null}
    {facts.length ? <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: rowHero ? 'flex-start' : 'center', columnGap: 12, rowGap: 6, marginTop: 8}}>
      {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, rowHero ? (wide ? 14.5 : 14) : 13, rowHero ? 400 : index === 0 ? 700 : 600), {color: index === 0 ? theme.colors.text : theme.colors.textSecondary}]}>{fact}</Text>)}
    </View> : null}
  </>;
  const versionChoice = versions.length > 1 ? <Focusable accessibilityRole="button" accessibilityLabel={t('next.reader.version')} focusKind="row"
    onPress={() => setVersionsOpen(true)} style={{marginTop: 12, alignSelf: rowHero ? 'flex-start' : 'center', flexDirection: 'row', alignItems: 'center', gap: 6, paddingVertical: 6, paddingHorizontal: 10}}>
    <Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{t('next.reader.version')}</Text>
    <Text style={[browseType(theme, 13, 700), {color: theme.colors.text}]}>{selectedVersion?.format.toUpperCase() ?? ''}</Text>
    <Icon name="i-down" size={14} color={theme.colors.textTertiary}/>
  </Focusable> : null;
  const actions = phone ? <>
    <BookDetailActions format={manifest.format} saved={saved} listed={listed} onList={onList} onMore={onMore} readMark={readMark} canDownload={canDownload} onRead={() => onRead(manifest)}
      onReadMark={onReadMark} onDownload={() => onDownload(manifest)} downloadRef={downloadRef} horizontalPadding={20} roundSize={46}/>
    {versionChoice ? <View style={{paddingHorizontal: 20}}>{versionChoice}</View> : null}
  </> : <View style={{paddingHorizontal: rowHero ? 0 : 20, marginTop: 18}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      {saved === undefined
        ? <Button icon="i-book" onPress={() => onRead(manifest)} style={{flex: 1}}>{t('next.reader.read')}</Button>
        : <ResumeButton icon="i-book" progress={saved} onPress={() => onRead(manifest)} style={{flex: 1}}>{t('next.cards.read')}</ResumeButton>}
      <Button variant="icon" icon={listed?.listed ? 'i-check' : 'i-plus'} iconOnly disabled={!listed}
        accessibilityLabel={t(listed?.listed ? 'next.home.removeMyList' : 'next.home.addMyList')}
        onPress={() => {if (listed) onList(listed);}} style={{height: 48, width: 48}}/>
      <Button ref={downloadRef} variant="icon" icon="i-dl" iconOnly disabled={!canDownload}
        accessibilityLabel={t('action.download')} onPress={() => onDownload(manifest)} style={{height: 48, width: 48}}/>
    </View>
    {versionChoice}
  </View>;
  const hero = rowHero
    ? <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: wide ? 36 : 28, paddingHorizontal: wide ? 32 : 28}}>
      {coverArt}
      <View style={{flex: 1, minWidth: 0}}>{copy}{actions}</View>
    </View>
    : <><View style={{alignItems: 'center', paddingHorizontal: 20}} onLayout={event => {
      const next = Math.round(event.nativeEvent.layout.height);
      if (next > 0) onHeroHeight(next);
    }}>{coverArt}{copy}</View>{actions}</>;
  const opening = manifest.metadata.openingLines ? <View style={{marginHorizontal: rowHero ? (wide ? 32 : 28) : 20, marginTop: 18}}>
    <Text style={[browseType(theme, 13, 700), {color: theme.colors.textTertiary}]}>{t('next.reader.firstLines')}</Text>
    <Text numberOfLines={4} style={[browseType(theme, 14.5, 400), {color: theme.colors.textSecondary, lineHeight: 23, marginTop: 6, paddingLeft: 14,
      borderLeftWidth: 2, borderLeftColor: alpha(theme.colors.text, .2)}]}>{manifest.metadata.openingLines}</Text>
  </View> : null;
  const panel = contents.length ? <View style={{marginTop: 14}}>
    <Tabs testID="native-read-book-tabs" value={tab} onValueChange={onTab} items={[
      {value: 'contents', label: t('next.reader.contents'), panel: <ContentsList entries={contents} currentSection={currentSection} onOpen={index => onRead(manifest, index)}/>},
      {value: 'details', label: t('next.reader.details'), panel: <DetailsList manifest={manifest} rows={detailFacts} series={series} comic={comic} onSeries={onSeries}/>},
    ]}/>
  </View> : <View style={{marginTop: 24, paddingHorizontal: 20}}>
    <Text style={[browseType(theme, 20, 700), {color: theme.colors.text, marginBottom: 6}]}>{t('next.reader.details')}</Text>
    <DetailsList manifest={manifest} rows={detailFacts} series={series} comic={comic} onSeries={onSeries} inset={false}/>
  </View>;
  const widePanel = contents.length ? <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: 36, paddingHorizontal: 32, marginTop: 38}}>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[browseType(theme, 20, 700, -.015), {color: theme.colors.text, marginBottom: 10}]}>{t('next.reader.contents')}</Text>
      <ContentsList entries={contents} currentSection={currentSection} onOpen={index => onRead(manifest, index)} wide/>
    </View>
    <View style={{width: 292, minWidth: 0}}>
      <Text style={[browseType(theme, 20, 700, -.015), {color: theme.colors.text, marginBottom: 10}]}>{t('next.reader.details')}</Text>
      <DetailsList manifest={manifest} rows={detailFacts} series={series} comic={comic} onSeries={onSeries} wide/>
    </View>
  </View> : <View style={{paddingHorizontal: 32, marginTop: 38}}>
    <Text style={[browseType(theme, 20, 700), {color: theme.colors.text, marginBottom: 6}]}>{t('next.reader.details')}</Text>
    <DetailsList manifest={manifest} rows={detailFacts} series={series} comic={comic} onSeries={onSeries}/>
  </View>;
  const versionsMenu = <Menu open={versionsOpen} onOpenChange={setVersionsOpen} title={t('next.reader.version')} items={versions.map(version => ({
    id: version.sourceId, label: version.format.toUpperCase(), description: defaultI18n.bytes(version.size),
    selected: version.sourceId === sourceId, disabled: !version.inventoryAvailable,
    onSelect: () => {setVersionsOpen(false); if (version.inventoryAvailable && version.sourceId !== sourceId) onVersion(manifest, versions, version.sourceId);},
  }))}/>;
  // Frame 2b's resolved styles inside .dev.ipad-land (tablet-read.html lines 782 and
  // 820-830 override kit.css): --gutter 32, .row margin-top 28, .row-head h2 21,
  // .track padding 8/32/10 gap 14, .card 142 wide (2:3 art 213), .card .t 13.5/600
  // nowrap margin-top 10, .card .s 13/400 margin-top 2. The native ScrollView has no
  // horizontal padding of its own, so the section's 32 is the screen-edge inset the
  // frame's .dev gutter asks for; nothing adds a second 64. Phone and upright keep
  // the values they were merged with.
  const expanded = wide;
  const inset = expanded ? 32 : 20;
  const shelfWidth = expanded ? 142 : theme.platform === 'phone' ? 118 : 150, shelfHeight = shelfWidth * 3 / 2;
  const shelfSection = series && (shelf.items.length > 0 || shelf.phase === 'error' || shelf.moreError) ? <View testID="native-read-book-series-shelf" style={{marginTop: expanded ? 28 : 30}}>
    <ReadingSelectionToolbar port={readingActions} runtime={runtime}/>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: expanded ? 14 : 10, paddingHorizontal: inset, marginBottom: expanded ? 14 : 10}}>
      <Text accessibilityRole="header" style={[browseType(theme, expanded ? 21 : 19, 700, -.015), {color: theme.colors.text}]}>{t('next.reader.restOfSeries')}</Text>
      <Text numberOfLines={1} style={[browseType(theme, expanded ? 14 : 13, expanded ? 400 : 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>{t('next.reader.seriesInOrder', {series: series.name})}</Text>
      <Focusable testID="native-read-book-series-all" accessibilityRole="link" accessibilityLabel={t('next.layout.seeAll')} hitSlop={10}
        onPress={() => onSeries(series, manifest)} style={{marginLeft: 'auto'}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: expanded ? 2 : 4}}>
          <Text style={[browseType(theme, expanded ? 14 : 13, 600), {color: expanded ? theme.colors.textTertiary : theme.colors.accentStrong}]}>{t('next.layout.seeAll')}</Text>
          <Icon name="i-right" size={expanded ? 16 : 14} color={expanded ? theme.colors.textTertiary : theme.colors.accentStrong}/>
        </View>
      </Focusable>
    </View>
    {shelf.phase === 'error' ? <StateView {...runtime.error(shelf.error, onShelfRetry, 'library')}/>
      : <ReadingShelf items={shelf.items} getKey={item => `${item.id}:${item.sourceId}`} width={shelfWidth} height={shelfHeight + 66}
        artHeight={shelfHeight} gap={expanded ? 14 : 10} gutter={inset} paddingTop={expanded ? 8 : 0} paddingBottom={expanded ? 10 : 4}
        pending={shelf.morePending} onNext={shelf.nextCursor && !shelf.moreError ? onShelfMore : undefined}
        renderItem={item => {const target = shelfOffer(item); return <VolumeCard item={item} width={shelfWidth} height={shelfHeight} comic={comic} theme={theme} runtime={runtime}
          variant={expanded ? 'shelf' : 'default'} onOpen={() => onShelfItem(item)} selection={target ? readingActions.selection(target) : undefined}
          onMenu={target ? anchorRef => readingActions.actions.show({...target, anchorRef}) : undefined}/>;}}/>}
    {shelf.moreError ? <StateView {...runtime.error(shelf.moreError, onShelfRetry, 'library')}/> : null}
  </View> : null;
  if (!wide) return <View style={{width: '100%', maxWidth: rowHero ? 820 : 640, alignSelf: 'center'}}>{hero}{opening}{panel}{shelfSection}{versionsMenu}</View>;
  return <View testID="native-read-book-split" style={{width: '100%', maxWidth: 1180, alignSelf: 'center'}}>
    {hero}{opening}{widePanel}{shelfSection}{versionsMenu}
  </View>;
}

function ContentsList({entries, currentSection, onOpen, wide = false}: {
  entries: NonNullable<ReadingManifest['contents']>; currentSection?: string; onOpen: (index: number) => void; wide?: boolean;
}) {
  const theme = useTheme();
  // tablet-read#2's wide split: the frame's computed row is 36 / 1fr / 70 at 52 high
  // (74 and a .08 fill for the current section) with a flat, unindented title. The
  // right 70 pt column is the printed page, which the contents DTO does not carry
  // (depth/fragment/section/title only), so it is not drawn and the honest ordinal
  // stays on the left instead of being moved into a page column.
  if (wide) return <View testID="native-read-book-contents">
    {entries.map((entry, index) => {
      const current = currentSection !== undefined && entry.section === currentSection;
      return <Focusable key={`${index}-${entry.section}`} accessibilityRole="button" accessibilityLabel={entry.title} onPress={() => onOpen(index)}
        focusKind="row" focusRadius={10}
        style={{minHeight: current ? 74 : 52, flexDirection: 'row', alignItems: 'center', columnGap: 14, paddingLeft: 8, paddingRight: 12, borderRadius: 10,
          ...(current ? {backgroundColor: alpha(theme.colors.text, .08)} : {})}}>
        <Text style={[browseType(theme, 14, 600), {width: 36, textAlign: 'center', color: current ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{index + 1}</Text>
        <Text numberOfLines={1} style={[browseType(theme, 15.5, entry.depth > 0 ? 500 : 600), {flex: 1, color: current ? theme.colors.accentStrong : theme.colors.text}]}>{entry.title}</Text>
      </Focusable>;
    })}
  </View>;
  const phone = theme.platform === 'phone';
  if (!phone) return <View testID="native-read-book-contents" style={{marginTop: 4, marginHorizontal: 20}}>
    {entries.map((entry, index) => {
      const current = currentSection !== undefined && entry.section === currentSection;
      return <Focusable key={`${index}-${entry.section}`} accessibilityRole="button" accessibilityLabel={entry.title}
        onPress={() => onOpen(index)} focusKind="row" focusRadius={8}
        style={{minHeight: 50, paddingVertical: 8, paddingLeft: 4 + Math.max(0, entry.depth) * 16, paddingRight: 4, justifyContent: 'center', ...ROW, borderTopColor: alpha(theme.colors.line, .7)}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <Text style={[browseType(theme, 13, 700), {color: current ? theme.colors.accentStrong : theme.colors.textTertiary, width: 20, textAlign: 'center'}]}>{index + 1}</Text>
          <Text numberOfLines={2} style={[browseType(theme, 15, 600), {color: current ? theme.colors.text : theme.colors.textSecondary, flex: 1}]}>{entry.title}</Text>
        </View>
      </Focusable>;
    })}
  </View>;
  return <View testID="native-read-book-contents" style={{marginTop: 6}}>
    {entries.map((entry, index) => {
      const current = currentSection !== undefined && entry.section === currentSection;
      return <Focusable key={`${index}-${entry.section}`} accessibilityRole="button" accessibilityLabel={entry.title}
        onPress={() => onOpen(index)} focusKind="row" focusRadius={10}
        style={{minHeight: current ? 68 : 50, paddingHorizontal: 20, paddingVertical: current ? 10 : 0, flexDirection: 'row', alignItems: 'center', columnGap: 12,
          ...(current ? {backgroundColor: alpha(theme.colors.text, .07)} : {})}}>
        <Text style={[browseType(theme, 13, 700), {width: 20, textAlign: 'center', color: theme.colors.textTertiary}]}>{index + 1}</Text>
        <Text numberOfLines={1} style={[browseType(theme, 15, current ? 700 : 600), {flex: 1, minWidth: 0, color: current ? theme.colors.accentStrong : theme.colors.textSecondary}]}>{entry.title}</Text>
      </Focusable>;
    })}
  </View>;
}

function DetailsList({manifest, rows, series, comic, onSeries, wide = false, inset = true}: {
  manifest: ReadingManifest; rows: readonly Readonly<{label: string; value: string}>[];
  series?: SeriesLink; comic: boolean; onSeries: (series: SeriesLink, manifest: ReadingManifest) => void; wide?: boolean; inset?: boolean;
}) {
  const theme = useTheme();
  // tablet-read#2's About column: a 92 pt label column, a 14 pt gap and a flexible
  // value at 14.5/400 with 24 pt between rows and no hairlines. 186 is only what a
  // 292 pt aside leaves; the value stays flexible at every width. The same rows and
  // the same guarded series link are drawn - no fact is added or inferred.
  if (wide) return <View testID="native-read-book-details" style={{rowGap: 24}}>
    {series ? <Focusable accessibilityRole="link" accessibilityLabel={series.name} onPress={() => onSeries(series, manifest)} focusKind="row" focusRadius={8}
      style={{flexDirection: 'row', columnGap: 14, alignItems: 'baseline'}}>
      <Text style={[browseType(theme, 14.5, 400), {width: 92, color: theme.colors.textTertiary}]}>{t('library.tab.series')}</Text>
      <View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 4}}>
        <Text numberOfLines={2} style={[browseType(theme, 14.5, 600), {color: theme.colors.text}]}>{series.name}</Text>
        {series.number !== undefined ? <Text style={[browseType(theme, 14.5, 600), {color: theme.colors.text}]}>{seriesNumber(series.number, comic)}</Text> : null}
      </View>
    </Focusable> : null}
    {rows.map(row => <View key={row.label + row.value} style={{flexDirection: 'row', columnGap: 14, alignItems: 'baseline'}}>
      <Text style={[browseType(theme, 14.5, 400), {width: 92, color: theme.colors.textTertiary}]}>{row.label}</Text>
      <Text style={[browseType(theme, 14.5, 400), {flex: 1, color: theme.colors.text}]}>{row.value}</Text>
    </View>)}
  </View>;
  if (theme.platform !== 'phone') return <View testID="native-read-book-details">
    {series ? <Focusable accessibilityRole="link" accessibilityLabel={series.name} onPress={() => onSeries(series, manifest)} focusKind="row" focusRadius={8}
      style={{paddingVertical: 10, ...ROW, borderTopColor: alpha(theme.colors.line, .7)}}>
      <Text style={[browseType(theme, 13.5, 600), {color: theme.colors.text}]}>{t('library.tab.series')}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 4, marginTop: 2}}>
        <Text style={[browseType(theme, 13.5, 400), {color: theme.colors.accentStrong}]}>{series.name}</Text>
        {series.number !== undefined ? <Text style={[browseType(theme, 13.5, 400), {color: theme.colors.accentStrong}]}>{seriesNumber(series.number, comic)}</Text> : null}
      </View>
    </Focusable> : null}
    {rows.map(row => <View key={row.label + row.value} style={{paddingVertical: 9, ...ROW, borderTopColor: alpha(theme.colors.line, .7)}}>
      <Text style={[browseType(theme, 13.5, 600), {color: theme.colors.text}]}>{row.label}</Text>
      <Text style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary, marginTop: 2}]}>{row.value}</Text>
    </View>)}
  </View>;
  const rowStyle = {position: 'relative' as const, flexDirection: 'row' as const, alignItems: 'baseline' as const, columnGap: 12, paddingVertical: 8, paddingHorizontal: 14};
  const detailType = browseType(theme, 14, 600, 0, 19.6);
  const separator = <View style={{position: 'absolute', top: 0, left: 14, right: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>;
  return <View testID="native-read-book-details" style={{marginTop: 14, marginHorizontal: inset ? 16 : 0, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
    {series ? <View style={rowStyle}>
      <Text style={[detailType, {width: 92, color: theme.colors.textTertiary}]}>{t('library.tab.series')}</Text>
      <Focusable accessibilityRole="link" accessibilityLabel={series.name} onPress={() => onSeries(series, manifest)} focusKind="row" focusRadius={8} style={{flex: 1, minWidth: 0}}>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 4}}>
          <Text numberOfLines={2} style={[detailType, {color: theme.colors.accentStrong}]}>{series.name}</Text>
          {series.number !== undefined ? <Text style={[detailType, {color: theme.colors.accentStrong}]}>{seriesNumber(series.number, comic)}</Text> : null}
        </View>
      </Focusable>
    </View> : null}
    {rows.map((row, index) => <View key={row.label + row.value} style={rowStyle}>
      {(series || index > 0) ? separator : null}
      <Text style={[detailType, {width: 92, color: theme.colors.textTertiary}]}>{row.label}</Text>
      <Text style={[detailType, {flex: 1, color: theme.colors.text}]}>{row.value}</Text>
    </View>)}
  </View>;
}
