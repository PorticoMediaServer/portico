import React from 'react';
import {Text, type StyleProp, type TextStyle} from 'react-native';
import {useTheme} from '../theme';
import {alpha} from './visual';

/** Search is already bounded by the canonical model. Only decorate its supplied label. */
export function HighlightedText({text, query, style, numberOfLines}: {text: string; query?: string; style?: StyleProp<TextStyle>; numberOfLines?: number}) {
  const {colors} = useTheme();
  const needle = query?.trim().toLocaleLowerCase('en-US') ?? '';
  const source = text.toLocaleLowerCase('en-US');
  const pieces: React.ReactNode[] = [];
  let offset = 0;
  if (needle.length) {
    let found = source.indexOf(needle, offset);
    while (found >= 0) {
      if (found > offset) pieces.push(text.slice(offset, found));
      pieces.push(<Text key={found} style={{color: colors.text, backgroundColor: alpha(colors.accent, .28)}}>{text.slice(found, found + needle.length)}</Text>);
      offset = found + needle.length;
      found = source.indexOf(needle, offset);
    }
  }
  pieces.push(text.slice(offset));
  return <Text style={style} numberOfLines={numberOfLines}>{pieces}</Text>;
}
