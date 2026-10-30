import React, {useEffect, useRef} from 'react';
import {Text, View} from 'react-native';
import {useTVKeyEvent} from '../../platform/tv-keys';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ProfileOption} from '../../kit/artwork';
import {FocusProvider} from '../../kit/focus';
import {nativeType, useTheme} from '../../kit/theme';
import {TVProfilePINLayout} from './TVAuthLayout';
import {TVCodeBoxes, TVNumericPad} from './TVNumericPad';

export type TVProfilePINFormProps = Readonly<{
  profile: ProfileOption; code: string; onCodeChange: (value: string) => void;
  busy: boolean; error?: React.ReactNode; hints?: React.ReactNode;
  onSubmit: (value: string) => void; onBack: () => void; onForgotPIN: () => void;
}>;

/** Profile PIN uses only the drawn numeric pad, never a TextInput or system keyboard. */
export function TVProfilePINForm(props: TVProfilePINFormProps) {
  return <FocusProvider><ProfilePINForm {...props}/></FocusProvider>;
}

/** Auth17's two static remote hints; account callers supply no layout or copy. */
export function TVProfilePINHints() {
  const theme = useTheme();
  return <>{([
    ['next.auth.tv.playPause', 'next.auth.tv.deleteDigit'],
    ['action.back', 'web.direct.otherProfiles'],
  ] as const).map(([key, caption]) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
    <View style={{minWidth: 48, height: 40, paddingHorizontal: 12, borderRadius: 10, borderWidth: 1.5,
      borderColor: `${theme.colors.text}4D`, alignItems: 'center', justifyContent: 'center'}}>
      <Text style={[nativeType({size: 20, weight: 800, lineHeight: 'normal', tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}]}>{defaultI18n.t(key)}</Text>
    </View>
    <Text style={[nativeType({size: 20, weight: 600, lineHeight: 'normal', tracking: 0}, theme.textScale), {color: theme.colors.textTertiary}]}>{defaultI18n.t(caption)}</Text>
  </View>)}</>;
}
function ProfilePINForm(props: TVProfilePINFormProps) {
  const id = `tv.auth.profile.${JSON.stringify(props.profile.id)}.pin`, submitted = useRef<string | undefined>(undefined);
  useEffect(() => {if (!props.code) submitted.current = undefined;}, [props.code]);
  const remove = () => {if (!props.busy) {submitted.current = undefined; props.onCodeChange(props.code.slice(0, -1));}};
  useTVKeyEvent(event => {if (event.key === 'playPause' && event.phase === 'press') remove();});
  const digit = (value: string) => {
    if (props.busy || props.code.length >= 4 || !/^\d$/.test(value)) return;
    const next = props.code + value;
    props.onCodeChange(next);
    if (next.length === 4 && submitted.current !== next) {submitted.current = next; props.onSubmit(next);}
  };
  return <TVProfilePINLayout profile={props.profile} error={props.error} hints={props.hints ?? <TVProfilePINHints/>} onBack={props.onBack}
    pad={<TVNumericPad id={id} busy={props.busy} onDigit={digit} onDelete={remove} help={defaultI18n.t('pin.forgot')} onHelp={props.onForgotPIN}/>}>
    <TVCodeBoxes code={props.code} length={4} masked busy={props.busy} error={!!props.error}/>
  </TVProfilePINLayout>;
}
