import React from 'react';
import type {RefObject} from 'react';
import {View} from 'react-native';
import {TrackRow, trackRowHeight, type TrackRowData, type TrackSelection, type TrackVariant} from '../browse';
import type {CardSelection} from '../layout/types';
import {PanelBlock, PanelHeading} from '../frames/title-parts';
import {SelectionCircle} from '../selection';
import {useTheme} from '../theme';
import {MusicWindow} from './MusicWindow';

export type TrackWindowProps = Readonly<{
  /** A heading over the rows, drawn with them as one section: quiet (phone-app frame 15b's "Top songs") or a section heading
   * with muted words at its end (collections 41a's "Chapters  17"). */
  heading?: Readonly<{title: string; quiet?: boolean; trailing?: string}>;
  rows: readonly TrackRowData[]; variant: Extract<TrackVariant, 'album' | 'top-songs' | 'chapters'>;
  /** A row's place in the page's selection, when the page has one. */
  select?: (id: string) => CardSelection | undefined;
  onPlay: (id: string) => void; onMore?: (id: string, anchor?: RefObject<View | null>) => void;
  /** Its rows open their place rather than play it (a book's chapters open the reader there). */
  opens?: boolean;
  /** The row that holds focus (a keyboard or switch on a phone), so the server's pages keep it while it is focused. */
  onFocusRow?: (id: string) => void; onBlurRow?: (id: string) => void;
  /** TV: false for a window already in its column (`TitleFrame`'s `beside`, an album's songs beside its cover): no gutter of its own. */
  inset?: boolean;
}>;

/** An album's songs or a book's chapters inside a music scroller (`musicScroller`; phone-app frame 13, collections 41a): its track rows, only the ones in view
 * mounted, as the old album screen windowed them. A row taller at a larger text size gets the room it needs. */
export function TrackWindow({heading, rows, variant, select, onPlay, onMore, onFocusRow, onBlurRow, inset = true, opens}: TrackWindowProps) {
  const theme = useTheme(), scale = Math.max(1, theme.fontScale * theme.textScale);
  // Built on every draw: a row's place in the selection changes without the rows changing.
  const selection: TrackSelection = {active: rows.some(row => select?.(row.id)?.active),
    isSelected: id => select?.(id)?.selected === true, onToggle: id => select?.(id)?.onToggle(), onEnter: id => select?.(id)?.onEnter(),
    renderControl: (row, selected) => <SelectionCircle selected={selected} selecting accessibilityLabel={row.text.title} variant="row" interactive={false}/>};
  // The chapter you are in is drawn taller (its progress under its title); the rows after it move down.
  const current = variant === 'chapters' ? rows.findIndex(row => row.current) : -1, height = Math.ceil(trackRowHeight(theme, variant) * scale);
  const window = <MusicWindow items={rows} itemHeight={height}
    expanded={current >= 0 ? {index: current, extra: Math.ceil(trackRowHeight(theme, variant, true) * scale) - height} : undefined}
    renderItem={(row, index) => <View onFocus={() => onFocusRow?.(row.id)} onBlur={() => onBlurRow?.(row.id)}>
      <TrackRow row={row} variant={variant} index={index} selection={selection} onPlay={onPlay} onMore={onMore} opens={opens}/></View>}/>;
  // On TV the rows sit in the page's gutter under their heading (tv-more frame 8); a phone row carries its own padding.
  const placed = theme.isTV && inset ? <PanelBlock>{window}</PanelBlock> : window;
  return heading ? <View><PanelBlock><PanelHeading quiet={heading.quiet} title={heading.title} trailing={heading.trailing}/></PanelBlock>{placed}</View> : placed;
}
