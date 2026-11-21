import React, {type ReactNode} from 'react';
import {ScrollView, View, useWindowDimensions} from 'react-native';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

/** A reading library's Magazines or Newspapers tab (tablet-read 1's pivots): its publications as 3:4 covers in rows that
 * wrap at the page's gutters, then what follows them (Show more). `cards` is given the card width that fills a row. */
export function PeriodicalShelf({cards, footer, testID}: {cards: (width: number) => ReactNode; footer?: ReactNode; testID?: string}) {
  const theme = useTheme(), frame = useFrameMetrics(), {width} = useWindowDimensions();
  const gap = theme.space.sm, inner = width - 2 * frame.gutter, target = theme.spec.card.width.poster[theme.column];
  const columns = Math.max(2, Math.floor((inner + gap) / (target + gap))), card = Math.floor((inner - gap * (columns - 1)) / columns);
  return <ScrollView testID={testID} contentContainerStyle={{paddingHorizontal: frame.gutter, paddingTop: theme.space.sm, paddingBottom: theme.space.xl, gap: theme.space.md}}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: gap, rowGap: theme.space.md}}>{cards(card)}</View>
    {footer}
  </ScrollView>;
}
