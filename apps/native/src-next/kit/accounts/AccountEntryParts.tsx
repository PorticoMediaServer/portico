import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
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

/** Signing this device in with a code another device approves: the QR code, the code, the address to type it at, and a note
 * (it refreshes by itself, or why it stopped). */
export function AccountEntryCode({qr, code, address, note}: {qr?: string; code: string; address?: string; note: string}) {
  const theme = useTheme();
  return <View style={{gap: 16, marginBottom: 20}}>{qr && <QRCode value={qr} size={theme.isTV ? 240 : 180}/>}
    <Text selectable style={[theme.type.title, {color: theme.colors.text}]}>{code}</Text>
    {address && <Text selectable style={[theme.type.body, {color: theme.colors.textSecondary}]}>{address}</Text>}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{note}</Text>
  </View>;
}

/** The Portico Account step's other ways in, stacked under its own action (providers, Create, Forgot, a local server). */
export function AccountEntryChoices({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 10, marginTop: 10}}>{children}</View>;
}

/** The Terms and Privacy line at the foot of the Portico Account step, links underlined in accent. */
export function AccountEntryChoicesLegal({onOpenLink}: {onOpenLink: (url: string) => void}) {
  const theme = useTheme();
  return <LegalAgreement style={[theme.type.caption, {color: theme.colors.textSecondary, textAlign: 'center', marginTop: 6}]}
    linkStyle={{color: theme.colors.accent, textDecorationLine: 'underline'}} onOpenLink={onOpenLink}/>;
}

/** A quiet sentence a sign-in step adds under its form (privacy, how codes work). */
export function AccountEntryNote({children, selectable = false}: {children: string; /** An address the person may copy. */ selectable?: boolean}) {
  const theme = useTheme();
  return <Text selectable={selectable || undefined} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** Quiet sentences stacked close under a form. */
export function AccountEntryNotes({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 4, marginTop: 4}}>{children}</View>;
}

/** "Check your email" after a reset link: how long the link lasts, then the privacy sentence, above the action. */
export function AccountEntrySentNotes({expiry, privacy}: {expiry: string; privacy: string}) {
  const theme = useTheme();
  return <>
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginBottom: 8}]}>{expiry}</Text>
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginBottom: 16}]}>{privacy}</Text>
  </>;
}

/** A quiet sentence under a step's actions. */
export function AccountEntryFootNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 10}]}>{children}</Text>;
}

/** Under a code step: Send the code again, centred, and how codes work. */
export function AccountEntryResend({action, help}: {action: ReactNode; help: string}) {
  const theme = useTheme();
  return <View style={{alignSelf: 'stretch', gap: 10, marginTop: 14}}>
    <View style={{alignSelf: 'center'}}>{action}</View>
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{help}</Text>
  </View>;
}
