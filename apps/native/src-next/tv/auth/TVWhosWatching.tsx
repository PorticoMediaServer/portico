import React from 'react';
import {Image, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {useTheme} from '../../kit/theme';
import {shellLayout} from '../../shell/model';

/** auth.html frame 16 (`.tvwho`): wordmark top-left on the round backdrop and one centred column.
 * The picker (heading and faces row) is the child; Back is the remote key, so no button is drawn. */
export function TVWhosWatching({children, testID}: {children: React.ReactNode; testID: string}) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ColourBackdrop shape="round" fill/>
      <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')}
        resizeMode="contain" style={{position: 'absolute', left: 120, top: 72, width: 174, height: 40}}/>
      <View style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, alignItems: 'center', justifyContent: 'center'}}>{children}</View>
    </View>
  </View>;
}
