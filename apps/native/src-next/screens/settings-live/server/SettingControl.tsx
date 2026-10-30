import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {FolderExplorer} from '../owner-libraries/FolderExplorer';
import {settingNumber} from './settings-values';

type Props = {row: SpecRow; value: unknown; disabled?: boolean; t: typeof defaultI18n.t; onChange: (value: unknown) => void; onValidity?: (valid: boolean) => void; onCustom?: () => void; api?: LibraryContentApi};

/** Only paints a checked bound value; page owners decide admission, visibility and saving. */
export function NativeSettingControl({row, value, disabled = false, t, onChange, onValidity, onCustom, api}: Props) {
  const theme = useTheme(), label = t(row.label), description = row.help ? t(row.help) : undefined;
  if (row.control.type === 'toggle') return <SettingsRow first label={label} description={description}
    control={<Switch testID={row.id} accessibilityLabel={label} checked={value === true} disabled={disabled} onCheckedChange={onChange}/>}/>;
  if (row.control.type === 'select' && row.control.options) {
    const options = row.control.options.map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined,
      disabled: option.locked || option.value === 'custom' && !onCustom}));
    if (!options.some(option => option.value === String(value))) options.push({value: String(value), label: String(value), description: undefined, disabled: true});
    return <SettingsRow first label={label} description={description} control={<Select testID={row.id} accessibilityLabel={label} value={String(value)} disabled={disabled}
      options={options} onValueChange={next => {
        if (next === 'custom') {onCustom?.(); return;}
        const option = row.control.options?.find(option => String(option.value) === next);
        if (option && !option.locked) onChange(option.value);
      }}/>}/>;
  }
  if (row.control.type === 'number') return <SettingNumber row={row} value={value} disabled={disabled} t={t} onChange={onChange} onValidity={onValidity}/>;
  if (row.control.type === 'text') return <TextField testID={row.id} label={label} description={description} value={typeof value === 'string' ? value : ''} editable={!disabled}
    autoCapitalize="none" autoCorrect={false} secureTextEntry={row.control.secret} placeholder={row.control.placeholder ? t(row.control.placeholder) : undefined} onChangeText={onChange}/>;
  if (row.control.type === 'folder') return <SettingFolder row={row} value={value} disabled={disabled} t={t} api={api} onChange={onChange}/>;
  if (row.control.type === 'matrix' && value && typeof value === 'object' && !Array.isArray(value)) {
    const values = value as Readonly<Record<string, unknown>>;
    return <View style={{gap: 8}}><Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label}</Text>
      {description && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{description}</Text>}
      {(row.control.items ?? []).map(item => item.id ? <Select key={item.id} testID={`${row.id}.${item.id}`} rowLabel={t(item.label)} value={String(values[item.id])} disabled={disabled}
        options={(row.control.columns ?? []).filter(column => column.value !== undefined).map(column => ({value: column.value!, label: t(column.label)}))}
        onValueChange={next => onChange({...values, [item.id!]: next})}/> : null)}
    </View>;
  }
  return null;
}

/** A folder row names one directory on the server and picks it through the one explorer. */
function SettingFolder({row, value, disabled, t, api, onChange}: Props) {
  const theme = useTheme(), [open, setOpen] = useState(false), label = t(row.label);
  const path = typeof value === 'string' && value.trim() ? value.trim() : undefined;
  const chosen = path ? [{path, name: path.split(/[\\/]/).filter(Boolean).pop() ?? path}] : [];
  return <>
    <SettingsRow first label={label} description={row.help ? t(row.help) : undefined}
      control={<View style={{alignItems: 'flex-end', gap: 6, maxWidth: '58%'}}>
        {path && <Text numberOfLines={1} style={[shellType(13.5, 600), {color: theme.colors.textSecondary}]}>{path}</Text>}
        {api && <Button testID={`${row.id}:choose`} size="xs" variant="glass" icon="i-folder" disabled={disabled} onPress={() => setOpen(true)}>{t('next.ownerLibraries.explorer.choose')}</Button>}
      </View>}/>
    {api && <FolderExplorer open={open} onOpenChange={setOpen} api={api} title={label} chosen={chosen} multiple={false}
      onUse={folders => {const next = folders[0]; if (next) onChange(next.path);}} testID={row.id}/>}
  </>;
}

function SettingNumber({row, value, disabled, t, onChange, onValidity}: Props) {
  const [text, setText] = useState(value === null ? '' : String(value)), [invalid, setInvalid] = useState(false);
  const validity = useRef(onValidity); validity.current = onValidity;
  useEffect(() => {setText(value === null ? '' : String(value)); setInvalid(false); validity.current?.(true);}, [value, disabled]);
  return <TextField testID={row.id} label={t(row.label)} description={row.help ? t(row.help) : undefined} value={text} editable={!disabled} keyboardType="numeric"
    placeholder={row.control.placeholder ? t(row.control.placeholder) : undefined} error={invalid ? t('error.invalidInput') : undefined}
    onChangeText={typed => {
      setText(typed); const result = settingNumber(typed, row.control); setInvalid(!result.valid); onValidity?.(result.valid);
      if (result.valid) onChange(result.value);
    }}/>;
}
