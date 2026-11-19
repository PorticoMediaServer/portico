import React, {useRef} from 'react';
import type {View} from 'react-native';
import type {ArtworkSource} from '../artwork';
import {PosterCard} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {useTheme} from '../theme';
import {Row} from './Row';
import type {RowPage} from './row-state';
import type {CardSelection, LayoutItemContext, LayoutSelection} from './types';

/** One title in a row of posters: what a page knows about it, never how big it is. */
export type PosterRowItem = Readonly<{key: string; title: string; year?: string; artwork?: ArtworkSource; watched?: boolean; progress?: number;
  onNavigate: () => void;
  /** Opens the title's menu from the card's own place (a popover on a tablet). */
  onMore?: (anchor: React.RefObject<View | null>) => void;
  selection?: CardSelection}>;
export type PosterRowProps = Readonly<{title: string; items: readonly PosterRowItem[]; paging?: RowPage<PosterRowItem>; testID?: string}>;

const resting: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};

/** A heading and a row of poster cards at the form's poster width (rule 14, `theme.spec.card.width.poster`): More like
 * this on a title page (phone-detail d1, tablet f6), and any other row of titles a page shows. The kit `Row` places and
 * spaces it on the frame; the page passes titles, never sizes. */
export function PosterRow({title, items, paging, testID}: PosterRowProps) {
  // The row reserves the captions' two lines, so a title's year never runs into the next section.
  const theme = useTheme(), width = theme.spec.card.width.poster[theme.column], caption = cardCaptionHeight(theme, 2);
  return <Row testID={testID} title={title} items={items} getKey={item => item.key} itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption} selection={resting} paging={paging}
    renderItem={(item, context) => <PosterItem item={item} context={context} width={width}/>}/>;
}

function PosterItem({item, context, width}: Readonly<{item: PosterRowItem; context: LayoutItemContext; width: number}>) {
  const anchor = useRef<View>(null), more = item.onMore;
  return <PosterCard focusId={context.focusId} testID={context.testID} onFocus={context.onFocus} onBlur={context.onBlur} width={width}
    title={item.title} caption={item.year !== undefined ? [{id: 'year', text: item.year, role: 'context'}] : undefined}
    artwork={item.artwork} watched={item.watched} progress={item.progress} selection={item.selection} onNavigate={item.onNavigate}
    menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}
