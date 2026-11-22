import React, {type ReactNode} from 'react';
import {View} from 'react-native';

/** The limits sheet's Cancel and Save, side by side. */
export function ProfileLimitsFooter({children}: {children: ReactNode}) {
  return <View style={{flexDirection: 'row', gap: 8}}>{children}</View>;
}

/** The limits sheet's fields. */
export function ProfileLimitsBody({children}: {children: ReactNode}) {
  return <View style={{gap: 14}}>{children}</View>;
}
