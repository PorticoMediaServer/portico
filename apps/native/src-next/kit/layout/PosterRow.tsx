import React, {useId, useRef} from 'react';
import type {View} from 'react-native';
import type {ArtworkSource} from '../artwork';
import {CircleCard, PosterCard, SquareCard} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {frameSpec} from '@portico/design';
import {useTheme} from '../theme';
import {Row} from './Row';
import type {RowPage} from './row-state';
import type {CardSelection, LayoutItemContext, LayoutSelection} from './types';

/** One title in a row of posters: what a page knows about it, never how big it is. */
export type PosterRowItem = Readonly<{key: string; title: string; year?: string; artwork?: ArtworkSource; watched?: boolean; progress?: number;
  /** A circle's covers (an artist's albums, drawn as the kit's artist circle); else its one artwork. */
  covers?: readonly ArtworkSource[];
  onNavigate: () => void;
  /** Opens the title's menu from the card's own place (a popover on a tablet). */
  onMore?: (anchor: React.RefObject<View | null>) => void;
  selection?: CardSelection}>;
export type PosterRowProps = Readonly<{title: string; items: readonly PosterRowItem[]; paging?: RowPage<PosterRowItem>;
  /** Smaller posters on a phone (a person's Known for, phone-detail d3 frame 7). */
  small?: boolean;
  /** Square covers at the form's square width (an album's More like this, an artist's albums), or circles at the form's
   * artist width (an artist's similar artists), as the old music pages drew them. */
  shape?: 'square' | 'circle';
  /** A quiet line beside the heading (the server's reason for the row). */
  subtitle?: string; testID?: string}>;

const resting: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};

/** A heading and a row of poster cards at the form's poster width (rule 14, `theme.spec.card.width.poster`): More like
 * this on a title page (phone-detail d1, tablet f6), and any other row of titles a page shows. The kit `Row` places and
 * spaces it on the frame; the page passes titles, never sizes. */
export function PosterRow({title, items, paging, small, shape, subtitle, testID}: PosterRowProps) {
  // The row reserves the captions' two lines, so a title's year never runs into the next section.
  const theme = useTheme(), caption = cardCaptionHeight(theme, 2);
  const width = shape === 'square' ? theme.spec.card.width.square[theme.column] : shape === 'circle' ? theme.spec.card.width.artist[theme.column]
    : small && theme.form === 'phone' ? frameSpec.hero.title.smallPoster : theme.spec.card.width.poster[theme.column];
  const artHeight = shape ? width : width * 1.5;
  // TV: the row is a focus region of its own, so it keeps its card when focus leaves it and comes back (tv-focus 3: "rows
  // keep their remembered card"), instead of landing on whichever poster lies nearest.
  const region = `poster-row.${useId()}`;
  return <Row testID={testID} title={title} subtitle={subtitle} items={items} focus={theme.isTV ? {id: region} : undefined} getKey={item => item.key} itemWidth={width} itemHeight={artHeight + caption} captionHeight={caption} selection={resting} paging={paging}
    renderItem={(item, context) => <PosterItem item={item} context={context} width={width} shape={shape}/>}/>;
}

function PosterItem({item, context, width, shape}: Readonly<{item: PosterRowItem; context: LayoutItemContext; width: number; shape?: 'square' | 'circle'}>) {
  const anchor = useRef<View>(null), more = item.onMore, Card = shape === 'square' ? SquareCard : PosterCard;
  if (shape === 'circle') return <CircleCard focusId={context.focusId} testID={context.testID} onFocus={context.onFocus} onBlur={context.onBlur} width={width}
    // A circle has no year under it: its name takes both caption lines rather than being cut (as the cast row's names).
    title={item.title} titleLines={2} art={{covers: item.covers ?? (item.artwork ? [item.artwork] : []), alt: item.title}} selection={item.selection} onNavigate={item.onNavigate}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
  // Two title lines, which the row's height already holds: a square cover's title is not cut either (owner's no-cut rule).
  return <Card focusId={context.focusId} testID={context.testID} onFocus={context.onFocus} onBlur={context.onBlur} width={width}
    title={item.title} titleLines={2} caption={item.year !== undefined ? [{id: 'year', text: item.year, role: 'context'}] : undefined}
    artwork={item.artwork} watched={item.watched} progress={item.progress} selection={item.selection} onNavigate={item.onNavigate}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}
