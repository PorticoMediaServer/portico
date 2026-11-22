import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Button} from '../controls';
import {TVAccountFields} from '../frames/TVAuthLayout';
import {useTheme} from '../theme';
import {LegalAgreement} from './LegalAgreement';

/** A sign-in step's fields: inside the TV account fields on a TV, one stacked column above the actions elsewhere. */
export function AccountEntryFields({children}: {children: ReactNode}) {
  const theme = useTheme();
  return theme.isTV ? <TVAccountFields>{children}</TVAccountFields> : <View style={{alignSelf: 'stretch', gap: 14, marginBottom: 20}}>{children}</View>;
}

/** The small line over first-run setup's title on a phone ("Set up this server"). */
export function AccountEntryStep({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.accountStep, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** The Terms of Use and Privacy line at the foot of the first sign-in page. */
export function AccountEntryLegal({onOpenLink}: {onOpenLink: (url: string) => void}) {
  const theme = useTheme();
  return <LegalAgreement style={[theme.type.accountLegal, {color: theme.colors.textTertiary, textAlign: 'center', marginTop: 'auto', paddingTop: 24}]}
    linkStyle={{color: theme.colors.accentStrong}} onOpenLink={onOpenLink}/>;
}

/** "Your Portico Account ended": said on the first sign-in page, above its choices. */
export function AccountEntryEndedNotice({children}: {children: string}) {
  const theme = useTheme();
  return <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.text, marginBottom: 16}]}>{children}</Text>;
}

/** Show or hide the password typed into the field it sits in. */
export function AccountEntryPasswordToggle({shown, label, disabled, onPress}: {shown: boolean; label: string; disabled: boolean; onPress: () => void}) {
  return <Button testID="accounts-password-visibility" variant="text" size="compact" iconOnly icon={shown ? 'i-eyeoff' : 'i-eye'} iconSize={18}
    accessibilityLabel={label} accessibilityState={{selected: shown}} disabled={disabled} style={{height: 40, width: 40, paddingHorizontal: 0}} onPress={onPress}/>;
}
