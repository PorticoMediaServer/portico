import React, {useCallback, useId, useRef, type ReactNode} from 'react';
import {View, type ViewStyle} from 'react-native';
import {frameSpec} from '@portico/design';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import type {ArtworkSource} from '../artwork';
import {PosterCard} from '../cards';
import {FocusRegion} from '../focus';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {WindowedGrid} from '../layout/WindowedGrid';
import {WindowedRow} from '../layout/WindowedRow';
import type {CardSelection, LayoutItemContext} from '../layout/types';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {browseGridCard} from './BrowseFrame';
import {useFrameMetrics} from './metrics';
import type {TitleCollapse} from './TitleFrame';
import {TitleListRow, titleListRowHeight, type TitleListItem} from './TitleListRow';
import {PanelBlock, PanelHeading} from './title-parts';

/** One poster in a page's grid: what the page knows about the title, never how big it is (as `PosterRowItem`). */
export type PosterGridItem = Readonly<{title: string; caption?: string; artwork?: ArtworkSource; watched?: boolean; progress?: number;
  onNavigate: () => void;
  /** Opens the title's menu from the card's own place. */
  onMore?: (anchor: React.RefObject<View | null>) => void;
  selection?: CardSelection}>;

export type PosterGridScrollerProps<T> = Readonly<{
  /** The titles, read a window at a time as the grid scrolls (an actor can have hundreds). */
  collection: WindowedCollection<T>;
  getKey: (value: T) => string;
  item: (value: T) => PosterGridItem;
  testID?: string;
}>;

/** A title page whose panel ends in a long grid of posters (a person's credits, phone-detail d3 frame 7): `TitleFrame`'s
 * `scroller`. The frame's page is the grid's header and the grid scrolls with it, windowed, at the browse grid's card
 * size (three across on a phone), with the frame's compact bar taking over at the frame's point. */
export function posterGridScroller<T>(props: PosterGridScrollerProps<T>) {
  return (content: ReactNode, style: ViewStyle, collapse: TitleCollapse) => <PosterGridScroller {...props} content={content} style={style} collapse={collapse}/>;
}

function PosterGridScroller<T>({collection, getKey, item, testID, content, style, collapse}: PosterGridScrollerProps<T> & Readonly<{content: ReactNode; style: ViewStyle; collapse: TitleCollapse}>) {
  const theme = useTheme(), frame = useFrameMetrics(), grid = frameSpec.browse.grid;
  const {width} = browseGridCard(frame), caption = cardCaptionHeight(theme, 2);
  const bottom = typeof style.paddingBottom === 'number' ? style.paddingBottom : 0, top = typeof style.paddingTop === 'number' ? style.paddingTop : 0;
  // The grid's scroll handler keeps the callback it was made with: the frame's collapse point arrives later (once the
  // name is measured), so the callback reads the latest one.
  const latest = useRef(collapse);
  latest.current = collapse;
  const onOffset = useCallback((offset: number) => latest.current.onChange(offset > latest.current.at), []);
  return <WindowedGrid testID={testID} collection={collection} getKey={getKey} header={content} style={{flex: 1}}
    // A card is its poster and its two caption lines (as the library grid): each row's stride makes room for the words.
    itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption} gap={grid.columnGap[frame.form]} rowGap={grid.rowGap[frame.form]}
    // The grid sits a heading's gap under the page's last part (the credits' filters).
    gutter={frame.gutter} rightGutter={frame.rightGutter} paddingTop={top + frame.headingGap} paddingBottom={bottom}
    onScrollOffset={onOffset}
    renderPlaceholder={() => <SkeletonBlock width={width} height={width * 1.5} radius={theme.radii.artwork}/>}
    renderItem={(value, context) => <GridPoster item={item(value)} context={context} width={width}/>}/>;
}

/** TV (tv-detail frame 10): a person's credits as a row per department, not a grid. A heading with its count (and, on the
 * first row, the page's Filters), then the titles read a window at a time as the row moves, at the form's poster width.
 * The row is a focus region of its own, so it keeps its card when focus leaves it and comes back (tv-focus 3). */
export function PosterWindowRow<T>({title, count, tools, collection, getKey, item, testID}: PosterGridScrollerProps<T> & Readonly<{title: string; count?: string; tools?: ReactNode}>) {
  const theme = useTheme(), frame = useFrameMetrics(), width = theme.spec.card.width.poster[theme.column], caption = cardCaptionHeight(theme, 2);
  // The heading's tools (f9: Filters) sit right after its count, not at the far end f9 draws them: there the remote never
  // reaches them from the row's first cards (tvOS moves by position), here Up from the row finds them above it.
  const region = `poster-window-row.${useId()}`;
  return <>
    <PanelBlock><View style={{flexDirection: 'row', alignItems: 'center', columnGap: theme.space.lg}}>
      <View style={{flexShrink: 1}}><PanelHeading title={title} count={count}/></View>{tools}
    </View></PanelBlock>
    <FocusRegion id={region} orientation="horizontal">
      <WindowedRow testID={testID} collection={collection} getKey={getKey} itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption}
        gutter={frame.gutter} rightGutter={frame.rightGutter}
        renderPlaceholder={() => <SkeletonBlock width={width} height={width * 1.5} radius={theme.radii.artwork}/>}
        renderItem={(value, context) => <GridPoster item={item(value)} context={{...context, focusId: context.id, testID: testID && `${testID}-${context.index}`}} width={width}/>}/>
    </FocusRegion>
  </>;
}

function GridPoster({item, context, width}: Readonly<{item: PosterGridItem; context: LayoutItemContext; width: number}>) {
  const anchor = useRef<View>(null), more = item.onMore;
  return <PosterCard focusId={context.focusId} testID={context.testID} onFocus={context.onFocus} onBlur={context.onBlur} width={width} density="grid"
    title={item.title} caption={item.caption !== undefined ? [{id: 'caption', text: item.caption, role: 'context'}] : undefined}
    artwork={item.artwork} watched={item.watched} progress={item.progress} selection={item.selection} onNavigate={item.onNavigate}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}

export type TitleRowsScrollerProps<T> = Readonly<{collection: WindowedCollection<T>; getKey: (value: T) => string; item: (value: T) => TitleListItem; testID?: string}>;

/** A title page whose panel ends in a long list of titles, one per row (a collection's films on a phone, phone-detail d3
 * frame 8): `TitleFrame`'s `scroller`, windowed like `posterGridScroller`, the rows at the page's width. */
export function titleRowsScroller<T>(props: TitleRowsScrollerProps<T>) {
  return (content: ReactNode, style: ViewStyle, collapse: TitleCollapse) => <TitleRowsScroller {...props} content={content} style={style} collapse={collapse}/>;
}

function TitleRowsScroller<T>({collection, getKey, item, testID, content, style, collapse}: TitleRowsScrollerProps<T> & Readonly<{content: ReactNode; style: ViewStyle; collapse: TitleCollapse}>) {
  const theme = useTheme(), frame = useFrameMetrics(), row = frameSpec.hero.title.parts.listRow;
  const width = frame.width - frame.gutter - frame.rightGutter, height = titleListRowHeight(theme);
  const bottom = typeof style.paddingBottom === 'number' ? style.paddingBottom : 0, top = typeof style.paddingTop === 'number' ? style.paddingTop : 0;
  const latest = useRef(collapse);
  latest.current = collapse;
  const onOffset = useCallback((offset: number) => latest.current.onChange(offset > latest.current.at), []);
  return <WindowedGrid testID={testID} collection={collection} getKey={getKey} header={content} style={{flex: 1}}
    itemWidth={width} itemHeight={height} gap={0} rowGap={0} gutter={frame.gutter} rightGutter={frame.rightGutter} paddingTop={top + frame.headingGap} paddingBottom={bottom}
    onScrollOffset={onOffset}
    renderPlaceholder={() => <SkeletonBlock width={row.poster} height={row.poster * 1.5} radius={theme.radii.artwork}/>}
    renderItem={value => <TitleListRow item={item(value)}/>}/>;
}
