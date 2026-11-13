import React from 'react';
import {ScrollView, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StandardBackdrop} from '../../kit/artwork';
import {TopBar} from '../../kit/layout/TopBar';
import {useTheme} from '../../kit/theme';

const t = defaultI18n.t;

/** Who's watching on the phone (design/ui-v1 final3-auth-05, frame 13): the kit picker carries the left-aligned
 * title, "On {server}", the faces, Add profile and Manage profiles; under it the remember row, a refusal and the
 * wait line. Sign-in and the app's own profile gate both draw this page. "‹ Back" shows only where there is a step
 * to go back to: the frame draws none, but taking it away would strand the person. */
export function PhoneWhosWatching({testID, picker, children, onBack, backDisabled}: {
  testID: string; picker: React.ReactNode; children?: React.ReactNode; onBack?: () => void; backDisabled?: boolean;
}) {
  const theme = useTheme();
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <StandardBackdrop/>
    {onBack ? <TopBar back={{label: t('action.back'), onPress: onBack, disabled: backDisabled, testID: `${testID}-back`}}/> : <TopBar/>}
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: 24, paddingTop: onBack ? 28 : 56, paddingBottom: 40}}>
      {picker}
      {children}
    </ScrollView>
  </View>;
}
