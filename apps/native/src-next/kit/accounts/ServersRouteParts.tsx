import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {TVAccountFields} from '../frames/TVAuthLayout';
import {useTheme} from '../theme';

/** Link a TV's choice of sign-in: the buttons stacked above the page's own actions. */
export function ServersConsentChoices({children}: {children: ReactNode}) {
  return <View style={{gap: 12, alignSelf: 'stretch', marginBottom: 20}}>{children}</View>;
}

/** Link a TV's code field: inside the TV account fields on a TV, above the actions elsewhere. */
export function ServersConsentCodeBox({children}: {children: ReactNode}) {
  const theme = useTheme();
  return theme.isTV ? <TVAccountFields>{children}</TVAccountFields> : <View style={{marginBottom: 20}}>{children}</View>;
}

/** What the TV asking to be signed in told us about itself, one line each. */
export function ServersConsentReview({lines}: {lines: readonly string[]}) {
  const theme = useTheme();
  return <View style={{marginBottom: 20}}>{lines.map((value, index) => <Text key={index} style={[theme.type.body, {color: theme.colors.text}]}>{value}</Text>)}</View>;
}

/** The chooser's quieter actions under the saved servers (Link a TV, Sign out of Portico Account). */
export function ServersFooter({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>{children}</View>;
}

/** Sign out and Forget's confirmation: the danger action, then Cancel. */
export function ServerSignOutFooter({label, busy, onConfirm, cancelLabel, onCancel}: {label: string; busy: boolean; onConfirm: () => void; cancelLabel: string; onCancel: () => void}) {
  const theme = useTheme();
  return <View style={{gap: 10}}>
    <Focusable focusId="sign-out-confirm" accessibilityRole="button" accessibilityLabel={label} disabled={busy}
      onPress={onConfirm} focusRadius={12} focusTreatment="none"
      style={{height: 54, borderRadius: 12, backgroundColor: `${theme.colors.danger}24`, alignItems: 'center', justifyContent: 'center'}}>
      <Text style={[theme.type.button, {color: theme.colors.danger}]}>{label}</Text>
    </Focusable>
    <Button focusId="sign-out-cancel" variant="text" disabled={busy} onPress={onCancel} textStyle={{color: theme.colors.textSecondary}}>{cancelLabel}</Button>
  </View>;
}

/** Sign out and Forget's lines: Forget's own line first, then what happens to downloads. */
export function ServerSignOutText({forgetBody, body}: {forgetBody?: string; body: string}) {
  const theme = useTheme();
  return <>
    {forgetBody !== undefined && <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 6}]}>{forgetBody}</Text>}
    <Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 14}]}>{body}</Text>
  </>;
}

/** Remove this server's downloads too: a check box and its line. */
export function ServerRemoveDownloads({checked, label, onToggle}: {checked: boolean; label: string; onToggle: () => void}) {
  const theme = useTheme();
  return <Focusable focusId="sign-out-remove-downloads" accessibilityRole="checkbox" accessibilityState={{checked}} accessibilityLabel={label}
    onPress={onToggle} focusRadius={6} focusTreatment="none" style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 8}}>
    <View style={{width: 24, height: 24, borderRadius: 6, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
      borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: checked ? theme.colors.accentStrong : 'transparent'}}>
      {checked && <Icon name="i-check" size={16} color={theme.colors.ink}/>}
    </View>
    <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{label}</Text>
  </Focusable>;
}

/** Sign out and Forget's lines and choice, inset like the sheet's title (other sheets pass the same 20 as contentStyle); a TV's
 * side sheet already pads its body. */
export function ServerSignOutBody({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: theme.isTV ? 0 : 20}}>{children}</View>;
}
