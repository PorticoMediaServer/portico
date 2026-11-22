import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Button, FormError} from '../controls';
import {shellType} from '../shell/visual';
import {useTheme} from '../theme';

/** A Portico Account security row's failure, with its Try again when the opening can still read again. */
export function HostedSignInFailure({message, retry}: {message: string; retry?: ReactNode}) {
  return <View style={{padding: 14, gap: retry ? 12 : undefined}}><FormError placement="inline">{message}</FormError>{retry}</View>;
}

/** A Google- or Apple-only account: Add a password waits behind this quiet button. */
export function HostedMoreSignIn({label, onPress}: {label: string; onPress: () => void}) {
  return <View style={{paddingHorizontal: 14, paddingVertical: 10}}><Button testID="settings-more-sign-in" variant="text" size="compact" style={{alignSelf: 'flex-start'}}
    onPress={onPress}>{label}</Button></View>;
}

/** Two-step's control: whether it is on, then its action. */
export function HostedTwoStepControl({status, children}: {status: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><Text style={[shellType(14.5), {color: theme.colors.textSecondary}]}>{status}</Text>{children}</View>;
}
