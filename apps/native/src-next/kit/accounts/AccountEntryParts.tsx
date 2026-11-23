import React, {type ReactNode} from 'react';
import {Image, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
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
  // Centred under the screen's centred title and intro: the QR, the code, the address and the note read as one block.
  return <View style={{gap: 16, marginBottom: 20, alignItems: 'center'}}>{qr && <QRCode value={qr} size={theme.isTV ? 240 : 180}/>}
    <Text selectable style={[theme.type.title, {color: theme.colors.text, textAlign: 'center'}]}>{code}</Text>
    {address && <Text selectable style={[theme.type.body, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{address}</Text>}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{note}</Text>
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

/** A step's facts and fields above its actions, stacked (`roomy` for a form with text fields: 14 instead of 12). */
export function AccountEntryDetails({children, roomy = false}: {children: ReactNode; roomy?: boolean}) {
  return <View style={{alignSelf: 'stretch', gap: roomy ? 14 : 12, marginBottom: 18}}>{children}</View>;
}

/** One line of a step's facts: `strong` (a name or heading), `primary` (body in the text colour), `secondary` (body, quieter)
 * or `caption` (small and quieter). */
export function AccountEntryLine({children, tone = 'secondary'}: {children: string; tone?: 'strong' | 'primary' | 'secondary' | 'caption'}) {
  const theme = useTheme();
  const type = tone === 'strong' ? theme.type.bodyStrong : tone === 'caption' ? theme.type.caption : theme.type.body;
  return <Text style={[type, {color: tone === 'strong' || tone === 'primary' ? theme.colors.text : theme.colors.textSecondary}]}>{children}</Text>;
}

/** The Portico wordmark over a joining step's title. */
export function AccountEntryWordmark({label}: {label: string}) {
  return <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={label} resizeMode="contain" style={{width: 74, height: 17}}/>;
}

/** The server's address over the identity check's title. */
export function AccountEntryAddress({children}: {children: string}) {
  const theme = useTheme();
  return <View><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{children}</Text></View>;
}

/** Scan a QR code instead: its hint, then the button. */
export function AccountEntryScan({hint, children}: {hint: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 6}}><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{hint}</Text>{children}</View>;
}

/** A step's quieter actions stacked under its own (`tight` for a list of links: 8). */
export function AccountEntryActions({children, tight = false}: {children: ReactNode; tight?: boolean}) {
  return <View style={{alignSelf: tight ? 'stretch' : undefined, gap: tight ? 8 : 12}}>{children}</View>;
}

/** A TV's "servers on this network" block under the address field. */
export function AccountEntryNearby({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 10, marginTop: 18}}>{children}</View>;
}

/** A line in that block: a `hint` (its heading, Loading, an address) or a `notice` (nothing found, or the search failed). */
export function AccountEntryNearbyLine({children, kind = 'hint', alert = false}: {children: string; kind?: 'hint' | 'notice'; alert?: boolean}) {
  const theme = useTheme(), tv = theme.isTV;
  const type = tv ? kind === 'hint' ? theme.type.accountHint : theme.type.accountLede : theme.type.caption;
  return <Text accessibilityRole={alert ? 'alert' : undefined} style={[type, {color: tv && kind === 'hint' ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{children}</Text>;
}

/** A server found on this network: its button, then its address. */
export function AccountEntryNearbyServer({children, host}: {children: ReactNode; host: string}) {
  return <View style={{gap: 4}}>{children}<AccountEntryNearbyLine>{host}</AccountEntryNearbyLine></View>;
}

/** A TV code screen's quiet line when the other device declined or the code failed. */
export function AccountEntryCodeNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginBottom: 28}]}>{children}</Text>;
}

/** The Portico Account's servers, one row each, stacked above the step's actions. */
export function AccountEntryServerList({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 12, marginBottom: 18}}>{children}</View>;
}

/** One server on the Portico Account: its name (and Owner), why it can't be reached, and an arrow; a server that is offline is dimmed. */
export function AccountEntryServerRow({name, owner, subtitle, offline, disabled, preferredFocus, accessibilityLabel, onPress}: {
  name: string; owner?: string; subtitle?: string; offline: boolean; disabled: boolean; preferredFocus: boolean; accessibilityLabel: string; onPress: () => void;
}) {
  const theme = useTheme();
  return <Focusable focusTreatment="none" disabled={disabled} preferredFocus={preferredFocus} accessibilityLabel={accessibilityLabel} onPress={onPress}
    style={({focused}) => ({flexDirection: 'row', alignItems: 'center', gap: 14, paddingHorizontal: 16, paddingVertical: 14, borderRadius: 14, borderWidth: 1.5,
      borderColor: focused ? theme.colors.accentStrong : theme.colors.line, backgroundColor: focused ? `${theme.colors.text}14` : `${theme.colors.text}0A`,
      opacity: offline ? .55 : 1})}>
    <Icon name="i-server" size={22} color={theme.colors.textSecondary}/>
    <View style={{flex: 1, gap: 2}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{name}</Text>
        {owner && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{owner}</Text>}</View>
      {subtitle && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{subtitle}</Text>}
    </View>
    <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>
  </Focusable>;
}
