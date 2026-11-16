import React from 'react';
import {View} from 'react-native';
import {TVAccountCanvas} from './TVAuthLayout';

/** auth16 keeps its centred picker inside the account family's shared canvas. */
export function TVWhosWatching({children, testID}: {children: React.ReactNode; testID: string}) {
  return <TVAccountCanvas testID={testID} shape="round">
    <View style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, alignItems: 'center', justifyContent: 'center'}}>{children}</View>
  </TVAccountCanvas>;
}
