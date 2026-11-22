import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useTheme} from '../theme';

/** A tuner's state at its row's end (Free, 1 in use, 1 clash), in its tone's colour. */
export function TunerState({text, tone}: {text: string; tone: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.captionStrong, {color: tone === 'healthy' ? theme.colors.healthy : tone === 'warning' ? theme.colors.amber : theme.colors.textSecondary}]}>{text}</Text>;
}

/** A row's small buttons side by side at its end (Pause, Remove). */
export function RowButtons({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', gap: theme.space.tight}}>{children}</View>;
}
