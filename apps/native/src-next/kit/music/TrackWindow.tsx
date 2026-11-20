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
  /** A quiet heading over the rows, drawn with them as one section (phone-app frame 15b's "Top songs"). */
  title?: string;
  rows: readonly TrackRowData[]; variant: Extract<TrackVariant, 'album' | 'top-songs'>;
  /** A row's place in the page's selection, when the page has one. */
  select?: (id: string) => CardSelection | undefined;
  onPlay: (id: string) => void; onMore?: (id: string, anchor?: RefObject<View | null>) => void;
  /** The row that holds focus (a keyboard or switch on a phone), so the server's pages keep it while it is focused. */
  onFocusRow?: (id: string) => void; onBlurRow?: (id: string) => void;
}>;

/** An album's songs inside a music scroller (`musicScroller`; phone-app frame 13): its track rows, only the ones in view
 * mounted, as the old album screen windowed them. A row taller at a larger text size gets the room it needs. */
export function TrackWindow({title, rows, variant, select, onPlay, onMore, onFocusRow, onBlurRow}: TrackWindowProps) {
  const theme = useTheme(), scale = Math.max(1, theme.fontScale * theme.textScale);
  // Built on every draw: a row's place in the selection changes without the rows changing.
  const selection: TrackSelection = {active: rows.some(row => select?.(row.id)?.active),
    isSelected: id => select?.(id)?.selected === true, onToggle: id => select?.(id)?.onToggle(), onEnter: id => select?.(id)?.onEnter(),
    renderControl: (row, selected) => <SelectionCircle selected={selected} selecting accessibilityLabel={row.text.title} variant="row" interactive={false}/>};
  const window = <MusicWindow items={rows} itemHeight={Math.ceil(trackRowHeight(theme, variant) * scale)}
    renderItem={(row, index) => <View onFocus={() => onFocusRow?.(row.id)} onBlur={() => onBlurRow?.(row.id)}>
      <TrackRow row={row} variant={variant} index={index} selection={selection} onPlay={onPlay} onMore={onMore}/></View>}/>;
  return title ? <View><PanelBlock><PanelHeading quiet title={title}/></PanelBlock>{window}</View> : window;
}
