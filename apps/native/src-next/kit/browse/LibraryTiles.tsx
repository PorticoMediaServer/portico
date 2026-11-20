import React from 'react';
import {Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {LibraryCard} from '../cards';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export function LibraryTiles({libraries, note}: {
  libraries: readonly (Pick<React.ComponentProps<typeof LibraryCard>, 'title' | 'shape' | 'covers' | 'glyph' | 'ambient' | 'caption'> & {id: string; onNavigate: () => void})[];
  note: string;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{paddingHorizontal: frame.cardInset, gap: frame.headerGap}}>
    {libraries.map(({id, ...library}) => <LibraryCard key={id} {...library} testID={`libraries-open-${encodeURIComponent(id)}`} focusId={`library:${id}`}
      width={Math.max(0, frame.width - 2 * frame.cardInset)} height={frameSpec.browse.libraryTile[frame.form]}/>)}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginHorizontal: theme.space.sm}]}>{note}</Text>
  </View>;
}
