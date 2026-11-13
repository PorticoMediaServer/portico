import React, {useRef} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ProfileFace, StandardBackdrop} from '../../kit/artwork';
import {Button, NumberPad} from '../../kit/controls';
import {TopBar} from '../../kit/layout/TopBar';
import {useTheme} from '../../kit/theme';
import {PhonePINEntry} from './PhonePINEntry';

const t = defaultI18n.t;

export type PhoneProfilePINProps = Readonly<{
  testID: string;
  profile: Readonly<{name: string; face: React.ComponentProps<typeof ProfileFace>}>;
  value: string; onChange: (value: string) => void; onSubmit: (pin: string) => void;
  busy: boolean; rejected: boolean;
  /** The wrong-PIN wait or other refusal (rules 28 and 29 of 01-sign-in), already worded by the caller. */
  status?: React.ReactNode;
  /** "Remember on this device", when this server allows it. */
  remember?: React.ReactNode;
  /** A line under Forgot PIN while the profile opens. */
  hints?: React.ReactNode;
  onForgot?: () => void; forgotDisabled?: boolean;
  onBack: () => void;
}>;

/** A profile's PIN on the phone (design/ui-v1 final3-auth-05, frame 14): "‹ Profiles", the profile's face, "Enter
 * profile PIN" and its name, four boxes, Forgot PIN centred, and the screen's own number pad. The fourth digit
 * opens the profile: there is no Continue. The launch picker, Switch profile and the app's lifecycle gate all use it. */
export function PhoneProfilePIN({testID, profile, value, onChange, onSubmit, busy, rejected, status, remember, hints, onForgot, forgotDisabled, onBack}: PhoneProfilePINProps) {
  const theme = useTheme(), submitted = useRef<string | undefined>(undefined);
  // Presses quicker than a render each build on the one before, never on the last drawn value.
  const latest = useRef(value);
  latest.current = value;
  if (!value) submitted.current = undefined;
  const type = (next: string) => {
    const digits = next.replace(/[^0-9]/g, '').slice(0, 4);
    latest.current = digits;
    onChange(digits);
    if (digits.length === 4 && submitted.current !== digits) {submitted.current = digits; onSubmit(digits);}
  };
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <StandardBackdrop/>
    <TopBar back={{label: t('profiles.title'), onPress: onBack, disabled: busy, testID: `${testID}-back`}}/>
    <ScrollView style={{flex: 1}} keyboardShouldPersistTaps="handled" contentContainerStyle={{alignItems: 'center', paddingHorizontal: 24, paddingTop: 12, paddingBottom: 24}}>
      <ProfileFace {...profile.face} size={92}/>
      <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text, textAlign: 'center', marginTop: 22}]}>{t('auth.enterProfilePin')}</Text>
      <Text numberOfLines={2} style={[theme.type.body, {color: theme.colors.textSecondary, textAlign: 'center', marginTop: 4}]}>{profile.name}</Text>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, marginTop: 28, marginBottom: 10}]}>{t('pin.fourDigit')}</Text>
      <PhonePINEntry testID={`${testID}-pin`} value={value} onChange={type} busy={busy} error={rejected} softKeyboard={false}/>
      {status ? <View style={{alignSelf: 'stretch', alignItems: 'center', marginTop: 10}}>{status}</View> : null}
      {remember ? <View style={{alignSelf: 'stretch', marginTop: 12}}>{remember}</View> : null}
      {onForgot ? <Button align="center" variant="link" testID={`${testID}-forgot`} disabled={busy || forgotDisabled} onPress={onForgot} style={{marginTop: 16}}>{t('pin.forgot')}</Button> : null}
      {hints}
    </ScrollView>
    <NumberPad testID={`${testID}-pad`} disabled={busy} onDigit={digit => type(latest.current + digit)} onDelete={() => type(latest.current.slice(0, -1))}/>
  </View>;
}
