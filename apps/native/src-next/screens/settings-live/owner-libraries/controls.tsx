import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useTheme} from '../../../kit/theme';
import {shellType} from '../../../kit/shell/visual';

/** A group's small title above its card, with an optional action at its end. */
export function SectionTitle({children, action}: {children: ReactNode; action?: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', marginTop: 22, marginBottom: 8, paddingHorizontal: 4}}>
    <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, flex: 1}]}>{children}</Text>
    {action}
  </View>;
}
