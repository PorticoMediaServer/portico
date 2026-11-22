import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Button, Focusable, TextField} from '../controls';
import {BottomSheet} from '../overlays';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

/** A setup step's fields, full width above its actions. */
export function SetupFields({children}: {children: ReactNode}) {
  return <View style={{alignSelf: 'stretch', gap: 14, marginBottom: 20}}>{children}</View>;
}

/** A setup field: the sign-in recipe on phones. */
export function SetupField({label, value, onChange, credential, editable}: {label: string; value: string; onChange: (next: string) => void;
  credential?: 'username' | 'new-password'; editable: boolean}) {
  const theme = useTheme();
  return <TextField label={label} value={value} onChangeText={onChange} appearance={theme.platform === 'phone' ? 'auth' : undefined}
    secureTextEntry={credential === 'new-password'} textContentType={credential === 'new-password' ? 'newPassword' : credential} autoComplete={credential} editable={editable}/>;
}

/** A quiet line of help. */
export function SetupCaption({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** Where the setup code is on each kind of install. */
export function SetupCodeFolders({lines}: {lines: readonly string[]}) {
  return <View testID="setup-code-folders" style={{gap: 4}}>{lines.map(line => <SetupCaption key={line}>{line}</SetupCaption>)}</View>;
}

/** A stack of choices. */
export function SetupChoices({children}: {children: ReactNode}) {
  return <View style={{gap: 12}}>{children}</View>;
}

/** One radio card: a title over its explanation, outlined in the accent when chosen. */
export function SetupChoice({title, body, checked, disabled, testID, icon, onPress}: {title: string; body: string; checked: boolean; disabled: boolean;
  testID?: string; icon?: 'i-shield' | 'i-globe'; onPress: () => void}) {
  const theme = useTheme();
  const text = <><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text><SetupCaption>{body}</SetupCaption></>;
  return <Focusable testID={testID} accessibilityRole="radio" accessibilityState={{checked}} disabled={disabled} onPress={onPress}
    style={{...(icon ? {flexDirection: 'row', gap: 12} : {}), padding: 14, borderWidth: 1, borderRadius: 14, borderColor: checked ? theme.colors.accentStrong : theme.colors.line}}>
    {icon ? <><Icon name={icon} size={22} color={checked ? theme.colors.accentStrong : theme.colors.textTertiary}/><View style={{flex: 1}}>{text}</View></> : text}
  </Focusable>;
}

/** Remote access: On, Private, Off side by side. */
export function SetupSegments({children}: {children: ReactNode}) {
  return <View style={{flexDirection: 'row', gap: 8}}>{children}</View>;
}

/** One segment. */
export function SetupSegment({label, checked, disabled, testID, onPress}: {label: string; checked: boolean; disabled: boolean; testID: string; onPress: () => void}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="radio" accessibilityState={{checked}} disabled={disabled} onPress={onPress}
    style={{flex: 1, alignItems: 'center', paddingVertical: 10, borderWidth: 1, borderRadius: 12, borderColor: checked ? theme.colors.accentStrong : theme.colors.line}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{label}</Text>
  </Focusable>;
}

/** Start over asks first where the setup was approved (auth.html 1e panels 3–4). */
export function StartOverSheet({open, onOpenChange, title, description, cancelLabel, confirmLabel, onCancel, onConfirm}: {open: boolean; onOpenChange: (open: boolean) => void;
  title: string; description: string; cancelLabel: string; confirmLabel: string; onCancel: () => void; onConfirm: () => void}) {
  return <BottomSheet open={open} onOpenChange={onOpenChange} title={title} description={description}
    footer={<View style={{flexDirection: 'row', gap: 12}}>
      <Button testID="accounts-setup-start-over-cancel" variant="glass" style={{flex: 1}} onPress={onCancel}>{cancelLabel}</Button>
      <Button testID="accounts-setup-start-over-confirm" variant="primary" style={{flex: 1}} onPress={onConfirm}>{confirmLabel}</Button>
    </View>}>{null}</BottomSheet>;
}
