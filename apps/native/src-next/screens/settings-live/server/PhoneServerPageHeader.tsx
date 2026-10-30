import React from 'react';
import {Text, View} from 'react-native';
import {Button} from '../../../kit/controls/Button';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';

/** owner-server phone pages: the compact sheet header precedes the server band.
 * Navigation and translated copy belong to the mounting Settings presenter; the band node
 * is the same authored server treatment the Server group uses, passed only for server pages. */
export function PhoneServerPageHeader({title, backLabel, onBack, band}: {
  title: string;
  backLabel: string;
  onBack: () => void;
  band?: React.ReactNode;
}) {
  const {colors} = useTheme();
  return <View testID="phone-server-page-header" style={{height: 56, paddingHorizontal: 10, justifyContent: 'center'}}>
    {band && <View accessible={false} pointerEvents="none" style={{position: 'absolute', inset: 0}}>{band}</View>}
    <View accessible={false} pointerEvents="none" style={{position: 'absolute', top: 6, alignSelf: 'center', width: 38, height: 5, borderRadius: 3, backgroundColor: colors.lineStrong}}/>
    <Text accessibilityRole="header" pointerEvents="none" style={[shellType(17, 700), {position: 'absolute', left: 0, right: 0, textAlign: 'center', color: colors.text}]}>{title}</Text>
    <Button variant="text" size="small" icon="i-left" onPress={onBack} accessibilityLabel={backLabel}
      style={{alignSelf: 'flex-start', minHeight: 38, paddingHorizontal: 6, marginLeft: -4}}
      textStyle={[shellType(16, 600), {color: colors.textSecondary}]}>{backLabel}</Button>
  </View>;
}
