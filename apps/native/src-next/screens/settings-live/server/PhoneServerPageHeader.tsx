import React from 'react';
import {View} from 'react-native';
import {TopBar} from '../../../kit/layout/TopBar';
import {useTheme} from '../../../kit/theme';

/** A page inside the Settings sheet (owner-server phone pages, Notifications, Switch server): the sheet's
 * grabber over the shared top bar. Navigation and translated copy belong to the mounting presenter; the band
 * node is the same authored server treatment the Server group uses, passed only for server pages. */
export function PhoneServerPageHeader({title, backLabel, onBack, band, trailing, backTestID, backDisabled}: {
  title: string;
  backLabel: string;
  onBack: () => void;
  band?: React.ReactNode;
  trailing?: React.ReactNode;
  backTestID?: string;
  backDisabled?: boolean;
}) {
  const {colors} = useTheme();
  return <View testID="phone-server-page-header" style={{paddingTop: 12}}>
    {band && <View accessible={false} pointerEvents="none" style={{position: 'absolute', inset: 0}}>{band}</View>}
    <View accessible={false} pointerEvents="none" style={{position: 'absolute', top: 6, alignSelf: 'center', width: 38, height: 5, borderRadius: 3, backgroundColor: colors.lineStrong}}/>
    <TopBar safeArea={false} back={{label: backLabel, onPress: onBack, testID: backTestID, disabled: backDisabled}} title={title} trailing={trailing}/>
  </View>;
}
