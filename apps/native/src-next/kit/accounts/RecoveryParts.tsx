import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useTheme} from '../theme';

/** The fields a recovery step asks for, full width above its actions. */
export function RecoveryFields({gap, children}: {gap?: 'tight' | 'loose'; children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', marginBottom: 12, ...(gap ? {gap: gap === 'tight' ? 10 : 12} : {})}}>{children}</View>;
}

/** Who checks what was typed (Portico Services), said quietly under the fields. */
export function RecoveryNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}
