import React from 'react';
import {View} from 'react-native';
import {readLibrarySettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button} from '../../controls/Button';
import {FormError} from '../../controls/FormError';
import {Select} from '../../controls/Select';
import {Switch} from '../../controls/Switch';
import {TextField} from '../../controls/TextField';
import {BottomSheet} from '../../overlays/BottomSheet';
import {SettingsRow} from '../../shell';
import {libraryRowOptions} from '../../../screens/settings-live/owner-libraries/library-settings-model';
import {PanelBlock, SectionCard} from '../../frames';
import {useOwnerLibrarySettingsRows, type Props} from '../../../pages/shared/settings/useOwnerLibrarySettingsRows';

const libraryPanel = settingsSpec.find(section => section.scope === 'server' && section.id === 'libraries')?.panels?.library;
export function LibrarySettingsRows(props: Props) {
  const {tab, document, busy, t, fetchLyrics} = props;
  const {editing, setEditing, text, setText, error, custom, mounted, read, change, rowVisible, number} = useOwnerLibrarySettingsRows(props);
  const renderRow = (row: SpecRow, index: number) => {
    if (row.control.type === 'action') return <SettingsRow key={row.id} testID={row.id} first={index === 0} label={t(row.label)} control={<Button variant="glass" size="small" disabled={busy} onPress={() => {if (!busy && mounted.current) void fetchLyrics?.(() => mounted.current);}}>{t(row.control.label!)}</Button>}/>;
    const fact = readLibrarySettingsBinding(row.id, document)!;
    const condition = row.enableWhen, actual = condition ? read(condition.id) : undefined;
    const disabled = busy || !!condition && ((condition.in !== undefined && !condition.in.includes(actual as never)) || (condition.notIn !== undefined && condition.notIn.includes(actual as never)));
    const label = t(row.label), help = row.help ? t(row.help) : undefined;
    const changed = fact.default !== undefined && JSON.stringify(fact.current) !== JSON.stringify(fact.default);
    const reset = changed ? <Button variant="text" size="xs" disabled={disabled} onPress={() => change(row, fact.default)}>{t('player.subtitles.positionReset')}</Button> : undefined;
    if (row.control.type === 'toggle') return <SettingsRow key={row.id} testID={row.id} first={index === 0} label={label} description={help}
      control={<Switch checked={fact.current === true} disabled={disabled} accessibilityLabel={label} onCheckedChange={value => change(row, value)}/>}/>;
    if (row.control.type === 'select') {
      const options = libraryRowOptions(row, document);
      const current = custom.has(row.id) ? 'custom' : fact.current;
      const offered = options.map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined, disabled: option.locked || option.value === 'custom' && row.id !== 'libraries.scanDepth' && row.id !== 'libraries.scrub'}));
      if (!offered.some(option => option.value === String(current))) offered.push({value: String(current), label: String(current), description: undefined, disabled: true});
      return <View key={row.id}><SettingsRow first={index === 0} label={label} description={help} control={<Select testID={row.id} value={String(current)} disabled={disabled} accessibilityLabel={label} options={offered}
        onValueChange={value => {const option = options.find(candidate => String(candidate.value) === value); if (option) change(row, option.value);}}/>}/>
        {reset && <PanelBlock>{reset}</PanelBlock>}</View>;
    }
    if (row.control.type === 'number') return <SettingsRow navigates key={row.id} testID={row.id} first={index === 0} label={label} description={help}
      value={`${fact.current}${row.control.unit ? ` ${t(row.control.unit)}` : ''}`} onPress={disabled ? undefined : () => {setEditing(row); setText(String(fact.current));}}/>;
    if (row.control.type === 'matrix') {
      const values = fact.current as Readonly<Record<string, string>>;
      return <View key={row.id}>{(row.control.items ?? []).filter(item => item.id && Object.hasOwn(values, item.id)).map((item, position) => <SettingsRow key={item.id} first={index === 0 && position === 0} label={t(item.label)}
        control={<Select testID={`${row.id}.${item.id}`} accessibilityLabel={t(item.label)} disabled={disabled} value={values[item.id!]!}
          options={(row.control.columns ?? []).filter(column => column.value).map(column => ({value: column.value!, label: t(column.label)}))}
          onValueChange={value => change(row, {[item.id!]: value})}/>}/>)}</View>;
    }
    return null;
  };
  return <>
    {libraryPanel?.tabs?.find(value => value.id === tab)?.groups?.map((group, index) => {
      const rows = group.rows.flatMap(row => [row, ...(row.children ?? [])]).filter(rowVisible); if (!rows.length) return null;
      return <SectionCard key={index} label={group.title ? t(group.title) : undefined}>{rows.map(renderRow)}</SectionCard>;
    })}
    {error && <FormError placement="field">{error}</FormError>}
    <BottomSheet keyboardAvoiding open={!!editing} onOpenChange={open => {if (!open) setEditing(undefined);}} title={editing ? t(editing.label) : ''}
      footer={<Button disabled={busy || number === undefined} onPress={() => {if (editing && number !== undefined) {change(editing, number); setEditing(undefined);}}}>{t('action.save')}</Button>}>
      <TextField value={text} onChangeText={setText} keyboardType="number-pad" label={editing ? t(editing.label) : ''} description={editing?.control.unit ? t(editing.control.unit) : undefined}/>
    </BottomSheet>
  </>;
}
