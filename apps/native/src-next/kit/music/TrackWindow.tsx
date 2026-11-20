import React from 'react';
import type {RefObject} from 'react';
import type {View} from 'react-native';
import {TrackRow, trackRowHeight, type TrackRowData, type TrackSelection, type TrackVariant} from '../browse';
import type {CardSelection} from '../layout/types';
import {SelectionCircle} from '../selection';
import {useTheme} from '../theme';
import {MusicWindow} from './MusicWindow';

export type TrackWindowProps = Readonly<{
  rows: readonly TrackRowData[]; variant: Extract<TrackVariant, 'album' | 'top-songs'>;
  /** A row's place in the page's selection, when the page has one. */
  select?: (id: string) => CardSelection | undefined;
  onPlay: (id: string) => void; onMore?: (id: string, anchor?: RefObject<View | null>) => void;
}>;

/** An album's songs inside a music scroller (`musicScroller`; phone-app frame 13): its track rows, only the ones in view
 * mounted, as the old album screen windowed them. A row taller at a larger text size gets the room it needs. */
export function TrackWindow({rows, variant, select, onPlay, onMore}: TrackWindowProps) {
  const theme = useTheme(), scale = Math.max(1, theme.fontScale * theme.textScale);
  // Built on every draw: a row's place in the selection changes without the rows changing.
  const selection: TrackSelection = {active: rows.some(row => select?.(row.id)?.active),
    isSelected: id => select?.(id)?.selected === true, onToggle: id => select?.(id)?.onToggle(), onEnter: id => select?.(id)?.onEnter(),
    renderControl: (row, selected) => <SelectionCircle selected={selected} selecting accessibilityLabel={row.text.title} variant="row" interactive={false}/>};
  return <MusicWindow items={rows} itemHeight={Math.ceil(trackRowHeight(theme, variant) * scale)}
    renderItem={(row, index) => <TrackRow row={row} variant={variant} index={index} selection={selection} onPlay={onPlay} onMore={onMore}/>}/>;
}
