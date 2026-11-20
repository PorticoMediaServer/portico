import React, {useEffect, useRef, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {HomeChannel, HomeRow} from '../../../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {homeCollectionCaption, homeRowPresentation} from '../../../../../../packages/client-core/src/presentation/home-spotlight';
import {homeContinuationCard, homeContinuationSource} from '../../../../../../packages/client-core/src/presentation/home-continuation';
import {homeEntryCapabilities} from '../../../../../../packages/client-core/src/home-selection';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {programmeFactParts, programmeTimeRange, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {continueBookFacts} from '../../../../../../packages/client-core/src/presentation/continue-books';
import {ArtworkPlacementContext} from '../../../platform/ArtworkPipeline';
import {ArtworkArrivalContext} from '../../../kit/artwork';
import {CollectionBanner, LiveCard, PosterCard, RankedCard, ReadResumeCard, ResumeCard, SquareCard, WideCard, type CardProps} from '../../../kit/cards';
import {cardTitleLine} from '../../../kit/cards/caption-metrics';
import {Row} from '../../../kit/layout';
import type {LayoutItemContext} from '../../../kit/layout/types';
import {itemSelection} from '../../../kit/layout/types';
import type {FocusRegionProps} from '../../../kit/focus';
import {LoadingPaint, SkeletonBlock} from '../../../kit/states';
import {useTheme, type Theme} from '../../../kit/theme';
import {homeActionDiagnostics} from '../../../platform/home-action-diagnostics';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {generatedPoster} from '../../../screens/home/generated-poster';
import {homeCardText, homePlayTarget} from '../../../screens/home/model';

/** Home's rows, shared by the rebuilt phone page (`pages/touch/home/HomePage`) and the tablet and TV Home
 * (`screens/home/HomeScreen`) until those forms are rebuilt: the row cards, the live row, the collection banner, the
 * pending skeleton row and the row's window registration. */

/** foldable.html `.phone.md .card .t{font-size:14px}` and `.card .s{font-size:12.5px}`. */
export const mediumCardType = {title: {fontSize: 14}, caption: {fontSize: 12.5}} as const;
/** foldable.html `.phone.md .row-head h2{font-size:21px}`; the row heading role carries 19. */
export const mediumHeadingStyle = {fontSize: 21} as const;

/** The drawn phone row sub-line is 13 px; the shared rowSubtitle role carries 14. */
export function homeRowSubtitle(theme: Theme) {
  return theme.layoutMode === 'phone' ? theme.type.caption : theme.type.rowSubtitle;
}

/** A deferred row is real descriptor data, not a Home response: its title, shape and
 * policy are already known, while its preview is still being read. Skeletons stay
 * non-focusable, so focus only ever lands on a card that actually arrived. */
export function HomePendingRow({row, labels, viewportWidth}: {row: HomeRow; labels: Parameters<typeof serverTextLabel>[0]; viewportWidth: number}) {
  const theme = useTheme(), presentation = homeRowPresentation(row), title = serverTextLabel(labels, presentation.title);
  const wide = row.artworkShape === 'landscape', square = row.artworkShape === 'square';
  const width = wide ? theme.spec.card.width.wide[theme.column] : square ? theme.spec.card.width.square[theme.column] : theme.spec.card.width.poster[theme.column];
  const height = wide ? width * 9 / 16 : square ? width + (theme.isTV ? 80 : 54) : width * 1.5 + (theme.isTV ? 80 : 48 + cardTitleLine(theme));
  const gap = theme.isTV ? 28 : 10, count = Math.max(1, Math.ceil(Math.max(0, viewportWidth - theme.gutter) / (width + gap)) + 1);
  return <View testID={`home-pending-${row.id}`} style={theme.isTV ? {marginBottom: 30} : undefined}>
    <View style={{paddingHorizontal: theme.gutter, marginBottom: theme.isTV ? 22 : 12}}><Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text></View>
    <LoadingPaint ready={false} style={{height, flexGrow: 0, flexShrink: 0, flexBasis: 'auto'}} skeleton={<View style={{flexDirection: 'row', gap, paddingLeft: theme.gutter, overflow: 'hidden'}}>{Array.from({length: count}, (_, index) =>
      <SkeletonBlock key={index} width={width} height={height} radius={theme.isTV ? 14 : 8} shine={index === 0}/>)}</View>}/>
  </View>;
}

export function HomeWindow({runtime, row, sectionOrder, visible, physicalIds, retainedIds, range, children}: {runtime: NativeShellRuntime; row: HomeRow; sectionOrder: number; visible: boolean; physicalIds?: readonly string[]; retainedIds?: readonly string[]; range?: {first: number; last: number; keys?: readonly string[]}; children: React.ReactNode}) {
  const {isTV} = useTheme(), rowId = row.id, latest = useRef({row, sectionOrder});
  latest.current = {row, sectionOrder};
  // Updating a measured range is not an unmount. Cleanup belongs only to this
  // row's lifetime; otherwise registration publishes recursively recreate it.
  useEffect(() => {
    if (isTV) return;
    runtime.setHomeWindow(row, sectionOrder, 0, Math.min(127, (row.live?.channels.length ?? row.entries.length) - 1), visible);
  }, [isTV, runtime, row, sectionOrder, visible]);
  useEffect(() => {
    if (!isTV) return;
    runtime.setHomeWindow(row, sectionOrder, range?.first ?? 0, range?.last ?? (row.collection ? 0 : -1), visible, physicalIds, retainedIds, range?.keys);
  }, [isTV, runtime, row, sectionOrder, visible, physicalIds, retainedIds, range]);
  // An unmount never throws (P2-74 E-tv-30); the runtime reports a registration failure itself.
  useEffect(() => () => {const current = latest.current; try {runtime.setHomeWindow(current.row, current.sectionOrder, 0, -1, false);} catch { /* reported by the runtime */ }}, [runtime, rowId]);
  return children;
}

const homeChannelKey = (item: HomeChannel) => JSON.stringify([item.channel.sourceId, item.channel.id]);
const homeChannelFocusId = (item: HomeChannel) => JSON.stringify(['channel', item.channel.sourceId, item.channel.id]);
const noPictureInPictureStore = () => () => {};

export function HomeCard({runtime, row, entry, rank, context, width, rowIndex, visible, preferred, medium = false, current}: {
  runtime: NativeShellRuntime; row: HomeRow; entry: ContentEntry; rank?: number; context: LayoutItemContext; width: number; rowIndex: number; visible: boolean; preferred: boolean; medium?: boolean; current: () => boolean;
}) {
  const theme = useTheme(), anchor = useRef<View>(null), target = homePlayTarget(row, entry);
  // HM-4: Continue Listening (and Continue your books) draw the continuation from the row's own
  // resume target, as web does; native passed `ordinary` for Continue Listening.
  const resume = row.resumes?.find(value => value.entryId === entry.id)?.target ?? entry;
  const text = homeContinuationCard(row.id, homeCardText(entry, row.kind === 'continue', runtime.text), homeCardText(homeContinuationSource(entry, resume), true, runtime.text));
  // The admitted PiP fact only names the Continue row's own card when the same entry and a
  // current fence are playing; a matching title in a new or recommendation shelf stays quiet.
  const continueRow = row.kind === 'continue';
  const pictureInPicture = useSyncExternalStore(runtime.playback?.subscribe ?? noPictureInPictureStore, () => {
    if (!continueRow) return '';
    const playback = runtime.playback?.getSnapshot();
    if (playback?.pictureInPicture?.active !== true || !playback.fence || !playback.entry) return '';
    return playback.entry.kind === entry.kind && playback.entry.libraryId === entry.libraryId && playback.entry.id === entry.id
      ? runtime.text.t('next.player.playingInPictureInPicture') : '';
  }, () => '');
  const cardText = pictureInPicture ? {...text, caption: [...text.caption, {id: 'picture-in-picture', text: pictureInPicture, role: 'status' as const}]} : text;
  const navigate = () => {if (current()) runtime.actions.navigate(entry);};
  const offers = homeEntryCapabilities(entry, {platform: theme.isTV ? 'tv' : 'phone-app', playbackAvailable: !!runtime.playback});
  const play = offers.playable && target?.playback ? () => {homeActionDiagnostics.action('home-card', 'primary-play'); if (current()) runtime.actions.play(entry, target.playback!);} : undefined;
  const actions = play && (theme.isTV || row.kind === 'continue') ? {primaryAction: 'play' as const, onPlay: play, onNavigate: navigate}
    : {primaryAction: 'navigate' as const, onNavigate: navigate, ...(play ? {onPlay: play} : {})};
  const props: CardProps = {...cardText, ...actions, width, visible, onFocus: context.onFocus, onBlur: context.onBlur, selection: context.selection, preferredFocus: preferred, testID: context.testID,
    ...(medium ? {titleTextStyle: mediumCardType.title, captionTextStyle: mediumCardType.caption, titleMargin: 9} : {}),
    menuAnchorRef: anchor, moreExpanded: runtime.selection(row.id).isMenuOpen?.(entry.id),
    accessibilityState: {expanded: !!runtime.selection(row.id).isMenuOpen?.(entry.id)},
    onMore: () => {if (current()) runtime.actions.more(entry, row.id, anchor);}};
  const art = runtime.artwork(row.artworkShape === 'landscape' ? entry.backdropUrl ?? entry.posterUrl : entry.posterUrl);
  if (row.id === 'continue_books') {
    // Continue your books (Q24): the Read lane's resume card. A book, comic or magazine opens
    // through the Reading entry; an audiobook resumes. Tapping is the action, so no play button.
    // An audiobook's chapter and time left follow the shared Home continuation rule (HM-4).
    const facts = continueBookFacts(entry, runtime.text.t, homeCardText(homeContinuationSource(entry, resume), true, runtime.text)), author = entry.author ?? (facts.reading ? undefined : entry.subtitle);
    const card = {title: entry.displayTitle ?? entry.title, caption: author ? [{id: 'author', text: author, role: 'identity' as const}] : [],
      detail: [...(facts.where ? [{id: 'where', text: facts.where, role: 'context' as const}] : []), ...(facts.left ? [{id: 'left', text: facts.left, role: 'context' as const}] : [])],
      progress: facts.progress, width, height: Math.round(booksCardSize(theme).height * (width / booksCardSize(theme).width)),
      visible, onFocus: context.onFocus, onBlur: context.onBlur, selection: context.selection, testID: context.testID, artwork: art,
      primaryAction: 'navigate' as const, onNavigate: !facts.reading && play ? play : navigate,
      menuAnchorRef: anchor, moreExpanded: runtime.selection(row.id).isMenuOpen?.(entry.id), onMore: () => {if (current()) runtime.actions.more(entry, row.id, anchor);},
      artworkFallback: generatedPoster(entry.title, width)};
    return facts.reading ? <ReadResumeCard {...card} kind={facts.shape === 'magazine' ? 'periodical' : facts.shape === 'newspaper' ? 'newspaper' : 'book'}/> : <ResumeCard {...card}/>;
  }
  // BOOT-014: Home card art fades in over the approved 240 ms.
  return <ArtworkPlacementContext.Provider value={{row: rowIndex + 1, column: context.index, group: row.id}}><ArtworkArrivalContext.Provider value={240}>
    {rank ? <RankedCard {...props} rank={rank} artwork={art} isNew={entry.new} watched={entry.watched}/>
      : row.artworkShape === 'landscape' ? <WideCard {...props} artwork={art} progress={cardText.progress} watched={entry.watched}/>
      : row.artworkShape === 'square' ? <SquareCard {...props} artwork={art} progress={cardText.progress} watched={entry.watched} isNew={entry.new}/>
      : <PosterCard {...props} artwork={art} watched={entry.watched} isNew={entry.new} artworkFallback={generatedPoster(entry.title, width)}/>}
  </ArtworkArrivalContext.Provider></ArtworkPlacementContext.Provider>;
}

/** The Continue your books card's drawn size: the resume card's own size per form (kit ResumeCard: 560 x 216 on TV, where
 * its type is TV-sized), the Reading library's on a tablet (392 x 176) and the phone's elsewhere (296 x 150). */
export function booksCardSize(theme: Theme) {
  return theme.isTV ? {width: 560, height: 216} : theme.platform === 'tablet' ? {width: 392, height: 176} : {width: 296, height: 150};
}

export function HomeCollectionCard({row, runtime, width, visible, current, onFocus}: {row: HomeRow; runtime: NativeShellRuntime; width: number; visible: boolean; current: () => boolean; onFocus: () => void}) {
  const anchor = useRef<View>(null), collection = row.collection!, t = runtime.text.t;
  return <CollectionBanner onFocus={onFocus} title={collection.collection.name} kindLabel={t('next.home.collection')}
    covers={collection.members.flatMap(entry => {const artwork = runtime.artwork(entry.posterUrl); return artwork ? [artwork] : [];})}
    body={homeCollectionCaption(collection, runtime.text)[0]?.text} width={width} backdrop={runtime.artwork(collection.backdropUrl)} ambient={runtime.ambient(collection.backdropUrl)} visible={visible}
    menuAnchorRef={anchor} moreExpanded={runtime.selection(row.id).isMenuOpen?.(collection.collection.id)} accessibilityState={{expanded: !!runtime.selection(row.id).isMenuOpen?.(collection.collection.id)}}
    selection={itemSelection(runtime.selection(row.id), collection.collection.id)} onMore={() => {if (current()) runtime.actions.collectionMore(collection, row.id, anchor);}}
    primaryAction="navigate" onNavigate={() => {if (current()) runtime.actions.openCollection(collection);}}/>;
}

export function HomeLiveRow({row, runtime, visible, itemWidth, onRange, onVisibleKeys, onItemFocus, current, focus, viewportWidth, tabletMedium, scrollMemory}: {row: HomeRow; runtime: NativeShellRuntime; visible: boolean; itemWidth: number; onRange: (first: number, last: number) => void; onVisibleKeys: (keys: readonly string[]) => void; onItemFocus: (item: HomeChannel) => void; current: () => boolean; focus?: Omit<FocusRegionProps, 'children' | 'order' | 'orientation'>; viewportWidth: number; tabletMedium: boolean; scrollMemory?: {offset: number}}) {
  const theme = useTheme(), live = row.live!, labels = {t: runtime.text.t, has: defaultI18n.has}, medium = theme.platform === 'tablet' && !theme.isTV, mediumGutter = tabletMedium ? 28 : 24;
  // Projection time, expiry and server-issued marks define the bounded row. No lineup scan.
  const observed = Date.parse(live.observedAt);
  if (serverNow() >= Date.parse(live.expiresAt)) return null;
  // The drawn live row carries its server-authored sub-line ("Synthetic Live TV").
  const subtitle = serverTextLabel(labels, row.subtitleText);
  const rowSubtitle = subtitle && theme.layoutMode === 'phone' && theme.platform === 'phone'
    ? <Text numberOfLines={1} style={[homeRowSubtitle(theme), {color: theme.colors.textTertiary, includeFontPadding: false}]}>{subtitle}</Text>
    : undefined;
  const title = serverTextLabel(labels, row.titleText, row.title);
  return <Row title={title} subtitle={rowSubtitle} items={live.channels} getKey={homeChannelKey} getFocusId={homeChannelFocusId} focus={focus} initialViewportWidth={viewportWidth} scrollMemory={scrollMemory}
    itemWidth={itemWidth} itemHeight={theme.isTV ? 225 : theme.layoutMode === 'desktop' ? theme.spec.card.live.height.web : theme.spec.card.live.height.phone} selection={runtime.selection(row.id)} onVisibleRange={onRange} onVisibleKeys={onVisibleKeys} onItemFocus={onItemFocus}
    gutter={medium ? mediumGutter : undefined} rightGutter={medium ? mediumGutter : undefined} gap={medium ? 14 : undefined} headingStyle={medium ? mediumHeadingStyle : undefined}
    onSeeAll={row.seeAll ? () => {if (current()) runtime.actions.seeAll(row.seeAll!, title);} : undefined}
    renderItem={(item: HomeChannel, context) => <HomeChannelCard rowId={row.id} item={item} context={context} runtime={runtime} current={current} visible={visible} observed={observed} width={itemWidth}/>}/>;
}

export function HomeChannelCard({rowId, item, context, runtime, current, visible, observed, width}: {rowId: string; item: HomeChannel; context: LayoutItemContext; runtime: NativeShellRuntime; current: () => boolean; visible: boolean; observed: number; width: number}) {
  const anchor = useRef<View>(null), channel = item.channel, programme = channel.programmes.find(value => Date.parse(value.start) <= observed && Date.parse(value.end) > observed);
  const parts: readonly FactPart[] = programme ? programmeFactParts({startMs: Date.parse(programme.start), endMs: Date.parse(programme.end), subtitle: programme.subtitle, flags: programme.flags, recording: !!programme.recordingId}, runtime.text, observed) : [];
  const range = programme && programmeTimeRange(Date.parse(programme.start), Date.parse(programme.end), runtime.text);
  // phone-app.html frame 2: on a phone the card says the channel's name (its number is in the mark) and when the
  // programme ends ("Ends 6:30 PM"), not the time left and the range. Tablet and TV keep their lines.
  const phone = useTheme().platform === 'phone';
  const ends: readonly FactPart[] = range ? [{id: 'ends', text: runtime.text.t('guide.ends', {time: range.end}), role: 'supplement'}] : [];
  return <LiveCard testID={context.testID} title={programme?.title ?? channel.name} channel={channel.name} number={phone ? undefined : channel.number} sourceName={phone ? undefined : item.source.name}
    mark={{id: channel.id, sourceId: channel.sourceId, ...channel.mark}} logo={runtime.artwork(channel.logoPath)} width={width} visible={visible}
    flags={parts.filter(part => part.role === 'status')} caption={phone ? ends : parts.filter(part => part.id === 'remaining')} range={phone ? undefined : range?.text}
    progress={programme ? (observed - Date.parse(programme.start)) / (Date.parse(programme.end) - Date.parse(programme.start)) : undefined}
    menuAnchorRef={anchor} moreExpanded={runtime.selection(rowId).isMenuOpen?.(JSON.stringify([item.channel.sourceId, item.channel.id]))} accessibilityState={{expanded: !!runtime.selection(rowId).isMenuOpen?.(JSON.stringify([item.channel.sourceId, item.channel.id]))}}
    primaryAction="navigate" onNavigate={() => {if (current()) runtime.actions.channel(item);}} onMore={() => {if (current()) runtime.actions.channelMore(item, anchor);}}
    onFocus={context.onFocus} onBlur={context.onBlur} selection={context.selection}/>;
}

