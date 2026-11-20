import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {readingResumePage, type ReadingItem} from '../../../../../packages/client-core/src';
import {readingDuration} from '../../../../../packages/client-core/src/reading-manifest-facts';
import type {useBooksLibrary} from '../../pages/shared/reading/useBooksLibrary';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeReadingActionOffer} from '../../screens/read/reading-actions';
import {ReadingMenuAnchor, type useReadingActions} from '../../screens/read/ReadingActions';
import {PhoneReadingBookCard} from './PhoneReadingBookCard';
import {ReadingLoading, ReadingGridSkeleton} from './ReadingLoading';
import {GeneratedCover, type ArtworkSource} from '../artwork';
import {ReadResumeCard} from '../cards';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {StateView} from '../states';
import {WindowedGrid} from '../layout';
import {TVRow} from '../layout/TVRow';
import {useTheme} from '../theme';

/** Where a Continue card's book is (SRV-A2): its page for a page book, nothing for an EPUB. */
function resumeDetail(progress: Parameters<typeof readingResumePage>[0]): {id: string; text: string; role: 'context'}[] {
  const page = readingResumePage(progress);
  return page === undefined ? [] : [{id: 'section', text: defaultI18n.t('next.reader.pageLabel', {page: defaultI18n.number(page)}), role: 'context'}];
}

const initial = (title: string): string => title.trim().slice(0, 1).toUpperCase() || '·';
const tint = (title: string) => generatedCoverTint(title);
const cover = (item: ReadingItem, width: number, height: number) => <GeneratedCover title={item.title}
  author={item.author?.value} glyph={initial(item.title)} tint={tint(item.title)} width={width} height={height}/>;
const authorPart = (item: ReadingItem) => item.author ? [{id: 'author', text: item.author.value, role: 'identity' as const}] : [];

type LibraryView = ReturnType<typeof useBooksLibrary>;

export function ReadingLibraryBody({id, onWidth, children}: {id: string; onWidth: (width: number) => void; children: ReactNode}) {
  return <View testID={`native-reading-library-${id}`} style={{flex: 1}} onLayout={event => onWidth(event.nativeEvent.layout.width)}>{children}</View>;
}

export function ReadingLibraryLoading({metrics}: {metrics: LibraryView['metrics']}) {
  const theme = useTheme();
  return <ReadingLoading ready={false} style={{flex: 1, minHeight: 480}} skeleton={<ReadingGridSkeleton width={metrics.width} columns={metrics.columns} gap={metrics.gap} gutter={theme.gutter}/>}/>;
}

export function ReadingDiscovery({model, runtime}: {model: LibraryView; runtime: NativeShellRuntime}) {
  const insets = useSafeAreaInsets(), {discoveryState, openDiscover, readingOffer, readingActions, showBooks, pageMore} = model;
  const {tablet} = model.metrics;
  return <ScrollView testID="reading-discover" contentContainerStyle={{paddingBottom: insets.bottom + 40}}>
    {discoveryState.phase === 'error' ? <StateView testID="reading-library-error" variant="inline" {...runtime.error(discoveryState.error, model.retry, 'library')}/> : null}
    {discoveryState.phase === 'loading' && !discoveryState.continueItems.length && !discoveryState.recent.length ? <ReadingLibraryLoading metrics={model.metrics}/> : null}
    {discoveryState.continueItems.length ? <DiscoveryRow first title={defaultI18n.t('next.cards.read')}>
      {discoveryState.continueItems.map(row => <ReadingMenuAnchor key={row.item.id}>{anchorRef => {const offered = readingOffer(row.item, 'discover'); return <ReadResumeCard testID={`reading-continue-${row.item.id}`} width={tablet ? 392 : 296} height={tablet ? 176 : 150}
        title={row.item.title} caption={authorPart(row.item)} detail={resumeDetail(row.progress)}
        progress={Math.min(0.999, Math.max(0, row.progress.location.progression))} palette={tint(row.item.title)}
        artwork={runtime.artwork(row.item.coverUrl)}
        artworkFallback={<View accessible={false} importantForAccessibility="no-hide-descendants">{cover(row.item, tablet ? 99 : 81, tablet ? 148 : 122)}</View>}
        disabled={row.item.inventoryAvailable !== true} onNavigate={() => openDiscover(row.item)} menuAnchorRef={anchorRef} selection={offered ? readingActions.selection(offered) : undefined}
        onMore={offered ? () => readingActions.actions.show({...offered, anchorRef}) : undefined}/>;}}</ReadingMenuAnchor>)}
    </DiscoveryRow> : null}
    {discoveryState.recent.length ? <DiscoveryRow title={defaultI18n.t('home.row.recentlyAdded')} first={!discoveryState.continueItems.length} onSeeAll={showBooks}
      onNearEnd={() => pageMore('recent')}>
      {discoveryState.recent.map(item => <DiscoveryCoverCard key={item.id} item={item} artwork={runtime.artwork(item.coverUrl)} width={tablet ? 142 : 118} onOpen={openDiscover} offer={readingOffer(item, 'discover')} actions={readingActions}/>)}
    </DiscoveryRow> : null}
    {discoveryState.shortReads.length ? <DiscoveryRow title={defaultI18n.t('next.reader.shortReads')} sub={defaultI18n.t('next.reader.underThreeHours')}
      onNearEnd={() => pageMore('short-reads')}>
      {discoveryState.shortReads.map(row => <DiscoveryCoverCard key={row.item.id} item={row.item} artwork={runtime.artwork(row.item.coverUrl)} width={tablet ? 142 : 118} onOpen={openDiscover} offer={readingOffer(row.item, 'discover')} actions={readingActions}
        detail={defaultI18n.t('next.reader.aboutDuration', {duration: defaultI18n.duration(readingDuration(row.estimatedSeconds))})}/>)}
    </DiscoveryRow> : null}
  </ScrollView>;
}

export function ReadingFallbackGrid({model, runtime}: {model: LibraryView; runtime: NativeShellRuntime}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const {booksState, booksWindow, openBooks, readingOffer, readingActions} = model;
  const {width, columns, gap, captionHeight} = model.metrics;
  return <View style={{flex: 1}}>
    {booksState.phase === 'error' ? <StateView testID="reading-library-error" variant="inline" {...runtime.error(booksState.error, model.retry, 'library')}/> : null}
    <ReadingLoading ready={booksState.items.length > 0 || booksState.phase === 'ready' || booksState.phase === 'error'} style={{flex: 1}}
      skeleton={<ReadingGridSkeleton width={width} columns={columns} gap={gap} gutter={theme.gutter}/>}>
      <WindowedGrid testID="reading-books" collection={booksWindow} getKey={item => `${item.id}:${item.sourceId}`} itemWidth={width} itemHeight={width * 1.5 + captionHeight} captionHeight={captionHeight}
        gap={gap} rowGap={gap} gutter={theme.gutter} paddingTop={theme.gutter} paddingBottom={insets.bottom + 40} style={{flex: 1}}
        header={booksState.phase === 'ready' && !booksState.items.length ? <StateView testID="reading-library-empty" variant="inline" title={defaultI18n.t('library.emptyLibrary')} body={defaultI18n.t('library.emptyLibraryBody')}/> : undefined}
        renderPlaceholder={() => <ReadingGridSkeleton width={width} columns={1} gutter={0} rows={1}/>}
        renderItem={item => <DiscoveryCoverCard item={item} artwork={runtime.artwork(item.coverUrl)} width={width} onOpen={openBooks} offer={readingOffer(item, 'books')} actions={readingActions}
          onCaptionLayout={model.reportCaptionHeight}/>}/>
    </ReadingLoading>
  </View>;
}

/** The drawn Read row heading and its horizontal track (phone-read frames 1-3). */
function DiscoveryRow({title, sub, first = false, onSeeAll, onNearEnd, children}: {title: string; sub?: string; first?: boolean; onSeeAll?: () => void; onNearEnd?: () => void; children?: ReactNode}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? 14 : 44}}>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 14, paddingHorizontal: theme.gutter, marginBottom: 14}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      {sub ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flexShrink: 1}]}>{sub}</Text> : null}
      {onSeeAll ? <Focusable accessibilityRole="button" accessibilityLabel={title} focusKind="row" onPress={onSeeAll}
        style={{width: 44, height: 44, marginLeft: -8, alignItems: 'flex-start', justifyContent: 'center'}}>
        <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
      </Focusable> : null}
    </View>
    {/* Within about two screens of the end, the row asks for its next page (WEB-053). */}
    <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} scrollEventThrottle={200}
      onScroll={onNearEnd ? ({nativeEvent: {contentOffset, layoutMeasurement, contentSize}}) => {
        if (contentOffset.x + layoutMeasurement.width * 3 >= contentSize.width) onNearEnd();
      } : undefined}
      contentContainerStyle={{gap: theme.platform === 'phone' ? 16 : 14, paddingHorizontal: theme.gutter, paddingTop: 10, paddingBottom: 14}}>{children}</ScrollView></TVRow>
  </View>;
}

/** One sparse-metadata cover: the drawn portrait art with the title and author
 * under it, using the catalogue's canonical generated cover. Books and comics
 * are drawn 2:3; a magazine or newspaper issue is drawn 3:4. */
function DiscoveryCoverCard({item, artwork, width, detail, onOpen, offer, actions, onCaptionLayout}: {
  item: ReadingItem; artwork?: ArtworkSource; width: number; detail?: string; offer?: NativeReadingActionOffer;
  actions: ReturnType<typeof useReadingActions>; onOpen: (item: ReadingItem) => void; onCaptionLayout?: (height: number) => void;
}) {
  return <PhoneReadingBookCard testID={`reading-item-${item.id}`} title={item.title} author={item.author?.value} detail={detail}
    width={width} artwork={artwork} periodical={item.publicationType?.value === 'magazine' || item.publicationType?.value === 'newspaper'}
    onOpen={() => onOpen(item)} disabled={item.inventoryAvailable !== true} selection={offer ? actions.selection(offer) : undefined}
    onMenu={offer ? anchorRef => actions.actions.show({...offer, anchorRef}) : undefined} onCaptionLayout={onCaptionLayout}/>;
}
