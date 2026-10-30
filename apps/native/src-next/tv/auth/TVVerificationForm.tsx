import React, {useEffect, useRef} from 'react';
import {Text, View} from 'react-native';
import {useTVKeyEvent} from '../../platform/tv-keys';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';
import {TVCodeBoxes, TVNumericPad} from './TVNumericPad';

export type TVVerificationFormProps = Readonly<{
  kind: 'portico' | 'local'; code: string; onCodeChange: (value: string) => void;
  recovery: boolean; recoveryCode: string; onRecoveryCodeChange: (value: string) => void;
  onRecoveryChange: (value: boolean) => void;
  busy: boolean; error?: React.ReactNode; onSubmit: (value: string) => void; onBack: () => void;
}>;
const recoveryCharacters = [...'ABCDEFGHIJKLMNOPQRSTUVWXYZ-1234567890'];
const recoveryRows = Array.from({length: Math.ceil(recoveryCharacters.length / 9)}, (_, row) => recoveryCharacters.slice(row * 9, row * 9 + 9));

/** Controlled same-account MFA; the transaction owner clears a rejected code and maps its shared error. */
export function TVVerificationForm(props: TVVerificationFormProps) {
  return <FocusProvider><VerificationForm {...props}/></FocusProvider>;
}
function VerificationForm(props: TVVerificationFormProps) {
  const theme = useTheme(), t = defaultI18n.t, id = `tv.auth.${props.kind}.factor`, submitted = useRef<string | undefined>(undefined);
  useEffect(() => {if (!props.code) submitted.current = undefined;}, [props.code]);
  const remove = () => {if (!props.busy) {submitted.current = undefined; props.onCodeChange(props.code.slice(0, -1));}};
  useTVKeyEvent(event => {
    if (!props.recovery && !props.busy && event.key === 'playPause' && event.phase === 'press') remove();
  });
  // The numeric pad owns one initial entry and retains its key through busy.
  // Preserve the separate recovery keyboard lifecycle.
  const entryRevision = useTVBusyEntryRevision(props.busy) * 2 + (props.recovery ? 1 : 0);
  useDestinationEntryFocus({visible: props.recovery && !props.busy, intent: 'enter', revision: entryRevision},
    {regionId: `${id}.keyboard.0`, childId: 'A'});
  const type = (size: number, weight: 400 | 600 | 700 | 800 = 400) => nativeType({size, weight, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  const digit = (value: string) => {
    if (props.busy) return;
    if (value === 'delete') {remove(); return;}
    if (props.code.length >= 6) return;
    const next = props.code + value;
    props.onCodeChange(next);
    if (next.length === 6 && submitted.current !== next) {submitted.current = next; props.onSubmit(next);}
  };
  const recoveryKey = (key: string) => {
    if (props.busy) return;
    props.onRecoveryCodeChange(key === 'delete' ? props.recoveryCode.slice(0, -1) : key === 'clear' ? '' : props.recoveryCode + (key === 'space' ? ' ' : key));
  };
  const key = (value: string, span = 1) => {
    const recovery = props.recovery, label = value === 'delete' ? t('action.delete') : value === 'space' ? t('next.states.key.Space') : value === 'clear' ? t('filter.clear') : value;
    return <Focusable key={value} focusId={value} testID={`${id}-${recovery ? 'keyboard' : 'pad'}-${value}`} disabled={props.busy}
      accessibilityLabel={label} onPress={() => {if (recovery) recoveryKey(value); else digit(value);}} focusRadius={recovery ? 14 : 20}
      style={{width: recovery ? (860 - 80) / 9 * span + 10 * (span - 1) : 148, height: recovery ? 72 : 118,
        alignItems: 'center', justifyContent: 'center', borderRadius: recovery ? 14 : 20, backgroundColor: `${theme.colors.text}${recovery ? '0D' : '12'}`}}>
      {value === 'delete' ? <Icon name="i-backspace" size={recovery ? 32 : 46} color={theme.colors.textSecondary}/>
        : <Text style={[type(recovery ? span > 1 ? 22 : 28 : 48, 700), {color: recovery && /^\d$/.test(value) ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{label}</Text>}
    </Focusable>;
  };
  const pad = !props.recovery && <TVNumericPad id={id} busy={props.busy} onDigit={digit} onDelete={remove}
    onHelp={() => props.onRecoveryChange(true)} helpTextSize={20}
    help={<>{t('services.signin.lostPhone')}{' '}{t('services.signin.recovery')}</>}/>;
  return <TVAuthLayout testID={`tv-${props.kind}-verification`} portico={props.kind === 'portico'}
    heading={t('web.direct.codeTitle')} body={t('services.signin.twoStep')} aside={pad}>
    {props.error && <View accessibilityRole="alert" style={{marginBottom: 16}}>{props.error}</View>}
    {!props.recovery ? <TVCodeBoxes code={props.code} length={6} busy={props.busy} error={!!props.error}/> : <>
      <Text style={[type(22, 700), {color: theme.colors.textTertiary, marginBottom: 8}]}>{t('services.signin.recovery')}</Text>
      <View style={{width: 860, height: 84, borderRadius: 16, paddingHorizontal: 28, flexDirection: 'row', alignItems: 'center',
        backgroundColor: `${theme.colors.ink}9E`, borderWidth: 1.5, borderColor: theme.colors.accentStrong}}>
        <Text numberOfLines={1} style={[type(32, 600), {color: theme.colors.text, flexShrink: 1}]}>{props.recoveryCode}</Text>
        {!props.busy && <View style={{width: 3, height: 40, borderRadius: 2, marginLeft: 3, backgroundColor: theme.colors.accentStrong}}/>}
      </View>
      <FocusRegion id={`${id}.keyboard`} enabled={!props.busy} style={{gap: 10, width: 860, marginTop: 24}}>
        {recoveryRows.map((row, index) => {
          const last = index === recoveryRows.length - 1, order = last ? [...row, 'space', 'delete', 'clear'] : row;
          return <FocusRegion key={index} id={`${id}.keyboard.${index}`} entry={row[0]} order={order} orientation="horizontal" enabled={!props.busy}
            edges={{up: index > 0 ? `${id}.keyboard.${index - 1}` : undefined, down: last ? `${id}.actions` : `${id}.keyboard.${index + 1}`}}
            style={{flexDirection: 'row', gap: 10}}>{row.map(value => key(value))}{last && <>{key('space', 3)}{key('delete')}{key('clear', 2)}</>}</FocusRegion>;
        })}
      </FocusRegion>
      <FocusRegion id={`${id}.actions`} entry="submit" enabled={!props.busy} edges={{up: `${id}.keyboard.${recoveryRows.length - 1}`}} style={{flexDirection: 'row', gap: 20, marginTop: 24}}>
        <Button focusId="submit" disabled={props.busy || !props.recoveryCode} loading={props.busy ? true : undefined} onPress={() => {if (!props.busy && props.recoveryCode) props.onSubmit(props.recoveryCode);}}>{t('action.continue')}</Button>
        <Button focusId="back" variant="glass" disabled={props.busy} onPress={props.onBack}>{t('action.back')}</Button>
      </FocusRegion>
    </>}
  </TVAuthLayout>;
}
