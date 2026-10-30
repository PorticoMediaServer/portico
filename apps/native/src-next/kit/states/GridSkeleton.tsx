import React from 'react';
import {View, useWindowDimensions, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {useTheme} from '../theme';
import {SkeletonBlock} from './SkeletonBlock';
import {gridWindow, showSkeleton} from './model';

export type GridSkeletonProps = Readonly<{
  elapsedMs: number;
  /** Scroll state of the real windowed grid, not a second independent scroller. */
  scrollOffset: number;
  viewportHeight: number;
  width?: number;
  totalCount?: number;
  columns?: number;
  cardWidth?: number;
  shape?: 'poster' | 'square' | 'wide';
  gap?: number;
  rowGap?: number;
  overscanRows?: number;
  captions?: boolean;
  captionLines?: 1 | 2;
  /** Return only actual arrived content. Missing slots remain non-focusable skeletons. */
  renderArrived?: (index: number) => React.ReactNode;
  accessibilityLabel?: string;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

/** Only the visible rows and a bounded overscan window are constructed. */
export function GridSkeleton({elapsedMs, scrollOffset, viewportHeight, width, totalCount, columns, cardWidth, shape = 'poster', gap, rowGap, overscanRows = 1, captions = true, captionLines = 1, renderArrived, accessibilityLabel, style, testID}: GridSkeletonProps) {
  const theme = useTheme();
  const window = useWindowDimensions();
  const contentWidth = width ?? Math.max(0, window.width - (theme.isTV ? theme.gutter + 96 : 32));
  const columnGap = gap ?? (theme.isTV ? 32 : 10);
  const rowSpace = rowGap ?? (theme.isTV ? 40 : 16);
  const cols = columns ?? Math.max(1, Math.floor((contentWidth + columnGap) / ((theme.isTV ? 200 : 108) + columnGap)));
  const itemWidth = cardWidth ?? (contentWidth - columnGap * (cols - 1)) / cols;
  const artHeight = itemWidth * (shape === 'poster' ? 1.5 : shape === 'wide' ? 9 / 16 : 1);
  const captionHeight = captions ? theme.isTV ? 74 : 40 : 0;
  const rowHeight = artHeight + captionHeight + rowSpace;
  const visible = gridWindow({columns: cols, rowHeight, viewportHeight, scrollOffset, totalCount, overscanRows});
  const show = showSkeleton(elapsedMs);
  return <View testID={testID} accessibilityLabel={accessibilityLabel ?? defaultI18n.t('status.loading')} accessibilityState={{busy: true}}
    style={[{position: 'relative', height: visible.height, width: contentWidth}, style]}>
    {Array.from({length: visible.count}, (_, offset) => {
      const index = visible.first + offset;
      const arrived = renderArrived?.(index);
      const missing = arrived === undefined || arrived === null || arrived === false;
      if (missing && !show) return null;
      return <View key={index} testID={testID ? `${testID}-slot-${index}` : undefined} style={{position: 'absolute', left: (index % cols) * (itemWidth + columnGap), top: Math.floor(index / cols) * rowHeight, width: itemWidth}}>
        {missing ? <>
          <SkeletonBlock width={itemWidth} height={artHeight} shine={offset === 0} />
          {captions && <>
            <SkeletonBlock width={`${[70, 60, 80, 55][index % 4]}%`} height={theme.isTV ? 20 : 11} radius={theme.isTV ? 10 : 6} style={{marginTop: theme.isTV ? 16 : 9}} />
            {captionLines === 2 && <SkeletonBlock width="35%" height={theme.isTV ? 16 : 11} radius={6} style={{marginTop: theme.isTV ? 8 : 7}} />}
          </>}
        </> : arrived}
      </View>;
    })}
  </View>;
}
