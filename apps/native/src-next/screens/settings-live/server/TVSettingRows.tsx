import React, {useEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Focusable, Switch} from '../../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../../kit/focus';
import {Banner, Menu, SidePanel} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {NativeTVKeyboard} from '../../search/TVKeyboard';
import {settingNumber} from './settings-values';

type Translate = typeof defaultI18n.t;
export type TVChoice = Readonly<{value: string; label: string; description?: string; disabled?: boolean}>;

/** tv-more 23: a row with a value opens its choices in the side sheet; OK sets one and closes. */
export function TVChoiceRow({testID, label, description, value, options, disabled, dependent, onChange}: {
  testID: string; label: string; description?: string; value: string; options: readonly TVChoice[];
  disabled?: boolean; dependent?: boolean; onChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const shown = options.find(option => option.value === value)?.label ?? value;
  return <>
    <SettingsRow testID={testID} label={label} description={description} value={shown} dependent={dependent}
      onPress={disabled ? undefined : () => setOpen(true)} readable={disabled}/>
    <Menu testID={`${testID}-sheet`} open={open && !disabled} onOpenChange={next => {if (!next) setOpen(false);}} presentation="sideSheet"
      title={label} description={description} initialFocusId={value}
      items={options.map(option => ({id: option.value, label: option.label, description: option.description, selected: option.value === value, disabled: option.disabled,
        onSelect: () => {setOpen(false); if (option.value !== value) onChange(option.value);}}))}/>
  </>;
}

/** A switch row flips in place with OK and never opens a sheet. */
export function TVSwitchRow({testID, label, description, checked, disabled, dependent, onChange}: {
  testID: string; label: string; description?: string; checked: boolean; disabled?: boolean; dependent?: boolean; onChange: (value: boolean) => void;
}) {
  return <SettingsRow label={label} description={description} dependent={dependent}
    control={<Switch testID={testID} accessibilityLabel={label} checked={checked} disabled={disabled} onCheckedChange={onChange}/>}/>;
}

export type TVEntryKind = 'text' | 'secret' | 'number';

/** A typed value: the row opens a held side panel with the remote keyboard. A secret row
 * never draws its value, only whether one is kept, and the sheet starts empty. */
export function TVEntryRow({testID, label, description, kind, value, configured, unset, control, maxLength, disabled, dependent, t, onSave}: {
  testID: string; label: string; description?: string; kind: TVEntryKind;
  /** The current text or number. Never passed for a secret. */
  value?: string; configured?: boolean; unset?: string; control?: SpecRow['control']; maxLength?: number;
  disabled?: boolean; dependent?: boolean; t: Translate;
  onSave: (value: string | number | null) => void;
}) {
  const [open, setOpen] = useState(false);
  const shown = kind === 'secret' ? configured ? t('next.tvServer.secretSet') : unset ?? t('next.tvServer.secretNotSet') : value || undefined;
  return <>
    <SettingsRow testID={testID} label={label} description={description} value={shown} dependent={dependent}
      onPress={disabled ? undefined : () => setOpen(true)} readable={disabled}/>
    <TVEntrySheet open={open && !disabled} testID={`${testID}-sheet`} title={label} description={description} kind={kind}
      initial={kind === 'secret' ? '' : value ?? ''} control={control} maxLength={maxLength} t={t}
      onClose={() => setOpen(false)} onSave={next => {setOpen(false); onSave(next);}}/>
  </>;
}

const lower = ['abcdefghi', 'jklmnopqr', 'stuvwxyz.', '123456789', '-_:/@?=&~'];
const textRows = (upper: boolean) => lower.map(row => [...(upper ? row.toUpperCase() : row)]);
const numberRows = [[...'123456789']];

function TVEntrySheet({open, testID, title, description, kind, initial, control, maxLength = 2048, t, onClose, onSave}: {
  open: boolean; testID: string; title: string; description?: string; kind: TVEntryKind; initial: string;
  control?: SpecRow['control']; maxLength?: number; t: Translate;
  onClose: () => void; onSave: (value: string | number | null) => void;
}) {
  const theme = useTheme(), registry = useFocusRegistry(), instance = React.useId(), id = `${testID}.${instance}`;
  const [text, setText] = useState(initial), [upper, setUpper] = useState(false);
  useEffect(() => {if (open) {setText(initial); setUpper(false);}}, [open, initial]);
  const keyboard = `${id}.keyboard`, fields = `${id}.fields`, footer = `${id}.footer`;
  const rows = useMemo(() => kind === 'number' ? numberRows : textRows(upper), [kind, upper]);
  const entry = React.useMemo(() => ({current: {requestTVFocus: () => {registry.focus({regionId: `${keyboard}.row.0`, childId: rows[0]![0]!});}}}), [keyboard, registry, rows]);
  const number = kind === 'number' && control ? settingNumber(text, control) : undefined;
  const valid = kind === 'number' ? number?.valid === true : kind === 'secret' ? text.length > 0 : true;
  const step = (delta: number) => {
    if (!control) return;
    const base = number?.valid && typeof number.value === 'number' ? number.value : control.min ?? 0;
    const next = Math.min(control.max ?? Number.MAX_SAFE_INTEGER, Math.max(control.min ?? 0, base + delta * (control.step ?? 1)));
    setText(String(next));
  };
  const save = () => {if (open && valid) onSave(kind === 'number' ? number!.value ?? null : kind === 'secret' ? text : text.trim());};
  const masked = useMemo(() => '•'.repeat([...text].length), [text]);
  return <SidePanel open={open} testID={id} width={680} topInset={0} showClose={false} title={title} description={description}
    initialFocusRef={entry} onOpenChange={next => {if (!next) onClose();}}
    titleTextStyle={shellType(44, 800, 'normal', -.025)} style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}
    footer={<FocusRegion id={footer} orientation="horizontal" entry={valid ? 'save' : 'cancel'} edges={{up: keyboard}} style={{flexDirection: 'row', gap: 16}}>
      <Button focusId="cancel" testID={`${testID}-cancel`} variant="glass" onPress={onClose} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button focusId="save" testID={`${testID}-save`} variant="primary" disabled={!valid} onPress={save} style={{flex: 1}}>{t(kind === 'secret' ? 'next.tvServer.replace' : 'action.save')}</Button>
    </FocusRegion>}>
    <View style={{gap: 16}}>
      <FocusRegion id={fields} orientation="horizontal" edges={{down: keyboard}} style={{flexDirection: 'row', gap: 12, alignItems: 'center'}}>
        {kind === 'number' && <Button focusId="decrease" testID={`${testID}-decrease`} variant="glass" accessibilityLabel={t('next.shell.native.decreaseSymbol')} onPress={() => step(-1)}>{t('next.shell.native.decreaseSymbol')}</Button>}
        <View accessible accessibilityLabel={`${title}: ${kind === 'secret' ? masked.length ? t('next.tvServer.secretTyped', {count: masked.length}) : '' : text}`}
          style={{flex: 1, minHeight: 80, justifyContent: 'center', borderRadius: 16, paddingHorizontal: 18, paddingVertical: 12, backgroundColor: 'rgba(237,245,250,.08)'}}>
          <Text numberOfLines={1} ellipsizeMode="head" style={[shellType(30, 700), {color: theme.colors.text}]}>{(kind === 'secret' ? masked : text) || ' '}</Text>
        </View>
        {kind === 'number' && <Button focusId="increase" testID={`${testID}-increase`} variant="glass" accessibilityLabel={t('next.shell.native.increaseSymbol')} onPress={() => step(1)}>{t('next.shell.native.increaseSymbol')}</Button>}
        {kind !== 'number' && <Focusable focusId="case" testID={`${testID}-case`} accessibilityRole="button" accessibilityLabel={t(upper ? 'next.tvServer.lowerCase' : 'next.tvServer.upperCase')}
          onPress={() => setUpper(value => !value)} focusRadius={14} style={{height: 72, minWidth: 96, borderRadius: 14, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(237,245,250,.05)'}}>
          <Text style={[shellType(22, 700), {color: theme.colors.textSecondary}]}>{t(upper ? 'next.tvServer.lowerCase' : 'next.tvServer.upperCase')}</Text>
        </Focusable>}
      </FocusRegion>
      {kind === 'number' && text !== '' && !valid && <Banner testID={`${testID}-error`} variant="notice" tone="error" placement="inline" message={t('error.invalidInput')}/>}
      <NativeTVKeyboard id={keyboard} testID={`${testID}-keyboard`} width={500} rows={rows} columns={9} lead="0"
        value={text} activation={{visible: open, intent: 'enter', revision: 0}} leftRegionId={fields} rightRegionId={footer}
        onChangeText={next => {if (open) setText((kind === 'number' ? next.replace(/\s/g, '') : next).slice(0, maxLength));}}/>
    </View>
  </SidePanel>;
}
