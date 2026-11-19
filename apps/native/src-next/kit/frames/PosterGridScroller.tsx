import React, {useCallback, useRef, type ReactNode} from 'react';
import type {View, ViewStyle} from 'react-native';
import {frameSpec} from '@portico/design';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import type {ArtworkSource} from '../artwork';
import {PosterCard} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {WindowedGrid} from '../layout/WindowedGrid';
import type {CardSelection, LayoutItemContext} from '../layout/types';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {browseGridCard} from './BrowseFrame';
import {useFrameMetrics} from './metrics';
import type {TitleCollapse} from './TitleFrame';

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
    itemWidth={width} itemHeight={width * 1.5} captionHeight={caption} gap={grid.columnGap[frame.form]} rowGap={grid.rowGap[frame.form]}
    // The grid sits a heading's gap under the page's last part (the credits' filters).
    gutter={frame.gutter} rightGutter={frame.rightGutter} paddingTop={top + frame.headingGap} paddingBottom={bottom}
    onScrollOffset={onOffset}
    renderPlaceholder={() => <SkeletonBlock width={width} height={width * 1.5} radius={theme.radii.artwork}/>}
    renderItem={(value, context) => <GridPoster item={item(value)} context={context} width={width}/>}/>;
}

function GridPoster({item, context, width}: Readonly<{item: PosterGridItem; context: LayoutItemContext; width: number}>) {
  const anchor = useRef<View>(null), more = item.onMore;
  return <PosterCard focusId={context.focusId} testID={context.testID} onFocus={context.onFocus} onBlur={context.onBlur} width={width} density="grid"
    title={item.title} caption={item.caption !== undefined ? [{id: 'caption', text: item.caption, role: 'context'}] : undefined}
    artwork={item.artwork} watched={item.watched} progress={item.progress} selection={item.selection} onNavigate={item.onNavigate}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}
