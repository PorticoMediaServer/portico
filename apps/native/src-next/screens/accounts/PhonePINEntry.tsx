import React, {useEffect, useRef} from 'react';
import {StyleSheet, TextInput, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {useTheme} from '../../kit/theme';

export type PhonePINEntryProps = Readonly<{
  value: string; onChange: (value: string) => void; onComplete?: (value: string) => void;
  busy?: boolean; error?: boolean; label?: string; autoFocus?: boolean; testID?: string;
  /** False when the screen draws its own number pad: a hardware keyboard and code autofill still reach the field, the on-screen keyboard stays down
   * (`inputMode` 'none': React Native's TextInput ignores showSoftInputOnFocus whenever an inputMode is given). */
  softKeyboard?: boolean;
}>;

/** Auth15's four boxes over one real numeric input: the platform number pad and
 * password-manager one-time-code fill reach it, and the fourth digit submits once. */
export function PhonePINEntry({value, onChange, onComplete, busy = false, error = false, label = defaultI18n.t('pin.fourDigit'), autoFocus = true, testID, softKeyboard = true}: PhonePINEntryProps) {
  const theme = useTheme(), input = useRef<TextInput>(null), submitted = useRef<string | undefined>(undefined);
  useEffect(() => {if (!value) submitted.current = undefined;}, [value]);
  const change = (next: string) => {
    const digits = next.replace(/[^0-9]/g, '').slice(0, 4);
    onChange(digits);
    if (digits.length === 4 && submitted.current !== digits) {submitted.current = digits; onComplete?.(digits);}
  };
  const shown = Math.min(value.length, 3);
  return <View testID={testID} style={{alignSelf: 'stretch', height: 68, justifyContent: 'center'}}>
    <View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', justifyContent: 'center', gap: 14}}>
      {[0, 1, 2, 3].map(index => <View key={index} testID={`${testID ?? 'phone-pin'}-box-${index}`}
        style={{width: 58, height: 68, borderRadius: 15, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
          backgroundColor: `${theme.colors.ink}9E`, borderColor: error ? theme.colors.danger : index === shown && !busy ? theme.colors.accentStrong : theme.colors.lineStrong}}>
        {index < value.length && <View style={{width: 11, height: 11, borderRadius: 5.5, backgroundColor: theme.colors.text}}/>}
      </View>)}
    </View>
    <TextInput ref={input} testID={testID ? `${testID}-input` : undefined} accessibilityLabel={label} value={value} onChangeText={change} editable={!busy}
      keyboardType="number-pad" inputMode={softKeyboard ? 'numeric' : 'none'} autoComplete="one-time-code" textContentType="oneTimeCode"
      maxLength={4} secureTextEntry caretHidden autoFocus={autoFocus} showSoftInputOnFocus={softKeyboard}
      style={[StyleSheet.absoluteFill, {opacity: 0}]}/>
  </View>;
}
