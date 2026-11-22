import React from 'react';
import {Text} from 'react-native';
import {useTheme} from '../theme';

/** Why a PIN was refused, under the TV's boxes in a line of its own height (TVCodeErrorLine): no margin of its own, so
 * nothing moves when it shows. */
export function PinStatusLine({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.danger}]}>{children}</Text>;
}
