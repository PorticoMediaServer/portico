import React from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {Button} from '../controls/Button';
import type {TopBarBack} from '../layout/TopBar';

export function ReaderBack({testID, label, onPress}: TopBarBack) {
  const {width} = useWindowDimensions(), {top} = useSafeAreaInsets();
  const size = width >= 700 ? 44 : 38;
  return <View pointerEvents="box-none" style={{position: 'absolute', left: 12, top: top + 8, zIndex: 20}}>
    <Button testID={testID} variant="icon" size="compact" icon="i-left" iconOnly accessibilityLabel={label}
      focusRadius={width >= 700 ? 22 : 19} onPress={onPress} style={{height: size, width: size, paddingHorizontal: 0}}/>
  </View>;
}
