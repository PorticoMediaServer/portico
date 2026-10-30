import React, {useEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {AccessibilityInfo, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../../kit/controls';
import {FocusItem, FocusRegion, useFocusRegistry} from '../../kit/focus';
import {createInitialRegionEntry} from '../../kit/focus/initial-entry';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';

const rows = [['1', '2', '3'], ['4', '5', '6'], ['7', '8', '9'], ['delete', '0']];

/** Auth17's one native-focus pad, shared by profile PIN and ruling28 MFA. */
export function TVNumericPad({id, busy, onDigit, onDelete, help, onHelp, helpTextSize = 26}: {
  id: string; busy: boolean; onDigit: (value: string) => void; onDelete: () => void;
  help: React.ReactNode; onHelp: () => void; helpTextSize?: number;
}) {
  const theme = useTheme(), registry = useFocusRegistry();
  const focusRevision = useSyncExternalStore(registry.subscribe, registry.getSnapshot, registry.getSnapshot);
  const entry = useMemo(() => createInitialRegionEntry(registry, `${id}.pad`), [registry, id]);
  useEffect(() => {entry.request({active: theme.isTV, primary: `${id}.pad`});}, [entry, focusRevision, id, theme.isTV]);
  return <View style={{position: 'absolute', right: 260, top: 0, bottom: 0, width: 484, justifyContent: 'center'}}>
    <FocusRegion id={`${id}.pad`} entry="1" order={rows.flat()} grid={{columns: 3, count: 11, shortRow: 'exit'}} edges={{down: `${id}.help`}}>
      {rows.map((row, index) => <View key={index}
        style={{flexDirection: 'row', gap: 20, marginBottom: index < 3 ? 20 : 0}}>
        {row.map((value, column) => <FocusItem key={value} focusId={value} cell={{index: index * 3 + column}}><Focusable focusId={value} testID={`${id}-pad-${value}`} disabled={busy} focusableWhenDisabled focusRadius={20}
          accessibilityLabel={value === 'delete' ? defaultI18n.t('action.delete') : value}
          onPress={() => {if (!busy) {if (value === 'delete') onDelete(); else onDigit(value);}}}
          style={{width: 148, height: 118, alignItems: 'center', justifyContent: 'center', borderRadius: 20, backgroundColor: `${theme.colors.text}12`}}>
          {value === 'delete' ? <Icon name="i-backspace" size={46} color={theme.colors.textSecondary}/>
            : <Text style={[nativeType({size: 48, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{value}</Text>}
        </Focusable></FocusItem>)}
      </View>)}
    </FocusRegion>
    <FocusRegion id={`${id}.help`} entry="help" edges={{up: `${id}.pad`}} style={{marginTop: 38}}>
      <Button focusId="help" variant="glass" disabled={busy} focusableWhenDisabled onPress={() => {if (!busy) onHelp();}}
        style={{height: 72, paddingHorizontal: 18, borderRadius: 14}} textStyle={{fontSize: helpTextSize * theme.textScale}}>{help}</Button>
    </FocusRegion>
  </View>;
}

export function TVCodeBoxes({code, length, masked = false, busy, error}: {code: string; length: 4 | 6; masked?: boolean; busy: boolean; error: boolean}) {
  const theme = useTheme();
  const count = Math.min(length, code.length), label = defaultI18n.t(masked ? 'pin.fourDigit' : 'auth.verificationCode');
  // The catalogue owns the field name. Only counts may leave this secure entry
  // through accessibility; individual PIN/verification digits stay hidden.
  const value = defaultI18n.t('pin.progress', {count, length});
  const previous = useRef({count, error});
  useEffect(() => {
    const changed = previous.current.count !== count || error && !previous.current.error;
    previous.current = {count, error};
    if (changed) AccessibilityInfo.announceForAccessibility([label, value, error ? defaultI18n.t(masked ? 'signIn.error.pinInvalid' : 'signIn.error.codeInvalid') : undefined].filter(Boolean).join('. '));
  }, [count, error, label, masked, value]);
  return <View accessible accessibilityLabel={label} accessibilityState={{busy}}
    accessibilityValue={{min: 0, max: length, now: count, text: value}} style={{flexDirection: 'row', gap: 18}}>
    {Array.from({length}, (_, index) => <View key={index} accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{width: 104, height: 116, borderRadius: 18,
      alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.ink}9E`, borderWidth: 2,
      borderColor: error ? theme.colors.danger : index === code.length ? theme.colors.accentStrong : theme.colors.lineStrong}}>
      {code[index] ? masked ? <View style={{width: 24, height: 24, borderRadius: 12, backgroundColor: theme.colors.text}}/>
        : <Text style={[nativeType({size: 28, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{code[index]}</Text>
        : index === code.length && !busy ? <View style={{width: 3, height: 52, borderRadius: 1, backgroundColor: theme.colors.accentStrong}}/> : null}
    </View>)}
  </View>;
}
