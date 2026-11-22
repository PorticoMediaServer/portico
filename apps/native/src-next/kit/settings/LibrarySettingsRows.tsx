import React from 'react';
import {Text, View} from 'react-native';
import type {SettingsRow as SpecRow} from '../../../../../packages/client-core/src/presentation/settings-spec';
import {Button} from '../controls/Button';
import {FormError} from '../controls/FormError';
import {Select} from '../controls/Select';
import {Switch} from '../controls/Switch';
import {TextField} from '../controls/TextField';
import {BottomSheet} from '../overlays/BottomSheet';
import {SettingsGroup, SettingsRow} from '../shell';
import {useTheme} from '../theme';
import {useLibrarySettingsRows, type LibrarySettingsRowsProps} from '../../pages/shared/settings/useLibrarySettingsRows';

/** A group's small title above its card (the settings section label role). */
function LibraryGroupTitle({children}: {children: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', marginTop: 22, marginBottom: theme.space.xs, paddingHorizontal: 4}}>
    <Text style={[theme.type.sectionLabel, {color: theme.colors.textTertiary, flex: 1}]}>{children}</Text>
  </View>;
}

/** A library's settings tab drawn from the spec: toggles, choices (with Reset to default), numbers in their own editor,
 * matrices of choices, and Fetch lyrics, in the spec's groups. */
export function LibrarySettingsRows(props: LibrarySettingsRowsProps) {
  const t = props.t, view = useLibrarySettingsRows(props), {busy, editing} = view;
  const renderRow = (row: SpecRow, index: number) => {
    if (row.control.type === 'action') return <SettingsRow key={row.id} testID={row.id} first={index === 0} label={t(row.label)}
      control={<Button variant="glass" size="small" disabled={busy} onPress={view.fetch}>{t(row.control.label!)}</Button>}/>;
    const fact = view.fact(row), disabled = view.disabled(row);
    const label = t(row.label), help = row.help ? t(row.help) : undefined;
    const changed = fact.default !== undefined && JSON.stringify(fact.current) !== JSON.stringify(fact.default);
    const reset = changed ? <Button variant="text" size="xs" disabled={disabled} onPress={() => view.change(row, fact.default)}>{t('player.subtitles.positionReset')}</Button> : undefined;
    if (row.control.type === 'toggle') return <SettingsRow key={row.id} testID={row.id} first={index === 0} label={label} description={help}
      control={<Switch checked={fact.current === true} disabled={disabled} accessibilityLabel={label} onCheckedChange={value => view.change(row, value)}/>}/>;
    if (row.control.type === 'select') {
      const {all, current, offered} = view.options(row);
      return <View key={row.id}><SettingsRow first={index === 0} label={label} description={help} control={<Select testID={row.id} value={String(current)} disabled={disabled} accessibilityLabel={label}
        options={offered.map(option => ({value: option.value, label: option.label ? t(option.label) : option.value, description: option.hint ? t(option.hint) : undefined, disabled: option.disabled}))}
        onValueChange={value => {const option = all.find(candidate => String(candidate.value) === value); if (option) view.change(row, option.value);}}/>}/>
        {reset && <View style={{alignItems: 'flex-end', paddingRight: 14}}>{reset}</View>}</View>;
    }
    if (row.control.type === 'number') return <SettingsRow navigates key={row.id} testID={row.id} first={index === 0} label={label} description={help}
      value={`${fact.current}${row.control.unit ? ` ${t(row.control.unit)}` : ''}`} onPress={disabled ? undefined : () => {view.setEditing(row); view.setText(String(fact.current));}}/>;
    if (row.control.type === 'matrix') {
      const values = fact.current as Readonly<Record<string, string>>;
      return <View key={row.id}>{(row.control.items ?? []).filter(item => item.id && Object.hasOwn(values, item.id)).map((item, position) => <SettingsRow key={item.id} first={index === 0 && position === 0} label={t(item.label)}
        control={<Select testID={`${row.id}.${item.id}`} accessibilityLabel={t(item.label)} disabled={disabled} value={values[item.id!]!}
          options={(row.control.columns ?? []).filter(column => column.value).map(column => ({value: column.value!, label: t(column.label)}))}
          onValueChange={value => view.change(row, {[item.id!]: value})}/>}/>)}</View>;
    }
    return null;
  };
  return <>
    {view.groups.map(group => <View key={group.key}>{group.title && <LibraryGroupTitle>{t(group.title)}</LibraryGroupTitle>}
      <SettingsGroup style={group.title ? undefined : {marginTop: 12}}>{group.rows.map(renderRow)}</SettingsGroup></View>)}
    {view.error && <FormError placement="field">{view.error}</FormError>}
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open) view.setEditing(undefined);}} title={editing ? t(editing.label) : ''}
      footer={<Button disabled={busy || view.number === undefined} onPress={view.saveNumber}>{t('action.save')}</Button>}>
      <TextField value={view.text} onChangeText={view.setText} keyboardType="number-pad" label={editing ? t(editing.label) : ''} description={editing?.control.unit ? t(editing.control.unit) : undefined}/>
    </BottomSheet>
  </>;
}
