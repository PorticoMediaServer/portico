import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import {patchLibrarySettingsBinding, readLibrarySettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {TextField} from '../../../kit/controls/TextField';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {useTheme} from '../../../kit/theme';
import {LibraryGroup, LibraryRow, SectionTitle} from './controls';
import {libraryNumberValue, libraryRowOptions, libraryRowVisible} from './library-settings-model';

const libraryPanel = settingsSpec.find(section => section.scope === 'server' && section.id === 'libraries')?.panels?.library;
type Props = {tab: 'general' | 'metadata' | 'scanning'; document: LibraryAdminDocument; busy: boolean; t: typeof defaultI18n.t; save: (settings: Partial<LibrarySettings>) => void; fetchLyrics?: (current: () => boolean) => Promise<void>};

/** Spec copy and control types, projected through the shared library writer. */
export function LibrarySettingsRows({tab, document, busy, t, save, fetchLyrics}: Props) {
  const theme = useTheme(), [editing, setEditing] = useState<SettingsRow>(), [text, setText] = useState(''), [error, setError] = useState<string>();
  const [custom, setCustom] = useState<ReadonlySet<string>>(new Set());
  const mounted = useRef(false);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const read = (id: string) => readLibrarySettingsBinding(id, document)?.current;
  const change = (row: SettingsRow, value: unknown) => {
    if (busy) return;
    if (value === 'custom') {
      setCustom(before => new Set([...before, row.id]));
      // A Custom preview still needs the preview runner; editing spacing alone
      // must not leave the saved preview mode Off.
      if (row.id === 'libraries.scrub' && read(row.id) === 'off') {
        try {setError(undefined); save(patchLibrarySettingsBinding(row.id, 'standard', document));}
        catch (problem) {setError(presentError(problem, 'library', {operation: 'save'}).body);}
      }
      return;
    }
    setCustom(before => {const next = new Set(before); next.delete(row.id); return next;});
    try {setError(undefined); save(patchLibrarySettingsBinding(row.id, value, document));}
    catch (problem) {setError(presentError(problem, 'library', {operation: 'save'}).body);}
  };
  const rowVisible = (row: SettingsRow) => {
    if (row.control.type === 'action') return row.id === 'libraries.fetchLyrics' && !!fetchLyrics && document.libraryKind === 'music' && document.eligibleControls.includes('libraries.lyrics') && document.settings.lyricsSource === 'lrclib';
    if (!libraryRowVisible(row.id, document) || read(row.id) === undefined) return false;
    if (tab === 'general' && row.id !== 'libraries.collections') return false;
    if (row.id === 'libraries.analysisSteps') return custom.has('libraries.scanDepth') || read('libraries.scanDepth') === 'custom';
    if (['libraries.scrubSpacing', 'libraries.scrubWidth', 'libraries.scrubMax'].includes(row.id)) return custom.has('libraries.scrub') || read('libraries.scrub') === 'custom';
    return true;
  };
  const renderRow = (row: SettingsRow, index: number) => {
    if (row.control.type === 'action') return <LibraryRow key={row.id} testID={row.id} first={index === 0} label={t(row.label)} trailing={<Button variant="glass" size="small" disabled={busy} onPress={() => {if (!busy && mounted.current) void fetchLyrics?.(() => mounted.current);}}>{t(row.control.label!)}</Button>}/>;
    const fact = readLibrarySettingsBinding(row.id, document)!;
    const condition = row.enableWhen, actual = condition ? read(condition.id) : undefined;
    const disabled = busy || !!condition && ((condition.in !== undefined && !condition.in.includes(actual as never)) || (condition.notIn !== undefined && condition.notIn.includes(actual as never)));
    const label = t(row.label), help = row.help ? t(row.help) : undefined;
    const changed = fact.default !== undefined && JSON.stringify(fact.current) !== JSON.stringify(fact.default);
    const reset = changed ? <Button variant="text" size="xs" disabled={disabled} onPress={() => change(row, fact.default)}>{t('player.subtitles.positionReset')}</Button> : undefined;
    if (row.control.type === 'toggle') return <LibraryRow key={row.id} testID={row.id} first={index === 0} label={label} help={help}
      trailing={<Switch checked={fact.current === true} disabled={disabled} accessibilityLabel={label} onCheckedChange={value => change(row, value)}/>}/>;
    if (row.control.type === 'select') {
      const options = libraryRowOptions(row, document);
      const current = custom.has(row.id) ? 'custom' : fact.current;
      const offered = options.map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined, disabled: option.locked || option.value === 'custom' && row.id !== 'libraries.scanDepth' && row.id !== 'libraries.scrub'}));
      if (!offered.some(option => option.value === String(current))) offered.push({value: String(current), label: String(current), description: undefined, disabled: true});
      return <View key={row.id}><LibraryRow first={index === 0} label={label} help={help} trailing={<Select testID={row.id} value={String(current)} disabled={disabled} accessibilityLabel={label} options={offered}
        onValueChange={value => {const option = options.find(candidate => String(candidate.value) === value); if (option) change(row, option.value);}} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/>
        {reset && <View style={{alignItems: 'flex-end', paddingRight: 14}}>{reset}</View>}</View>;
    }
    if (row.control.type === 'number') return <LibraryRow key={row.id} testID={row.id} first={index === 0} label={label} help={help}
      value={`${fact.current}${row.control.unit ? ` ${t(row.control.unit)}` : ''}`} onPress={disabled ? undefined : () => {setEditing(row); setText(String(fact.current));}}/>;
    if (row.control.type === 'matrix') {
      const values = fact.current as Readonly<Record<string, string>>;
      return <View key={row.id}>{(row.control.items ?? []).filter(item => item.id && Object.hasOwn(values, item.id)).map((item, position) => <LibraryRow key={item.id} first={index === 0 && position === 0} label={t(item.label)}
        trailing={<Select testID={`${row.id}.${item.id}`} accessibilityLabel={t(item.label)} disabled={disabled} value={values[item.id!]!}
          options={(row.control.columns ?? []).filter(column => column.value).map(column => ({value: column.value!, label: t(column.label)}))}
          onValueChange={value => change(row, {[item.id!]: value})} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/>)}</View>;
    }
    return null;
  };
  const number = editing ? libraryNumberValue(text, editing) : undefined;
  return <>
    {libraryPanel?.tabs?.find(value => value.id === tab)?.groups?.map((group, index) => {
      const rows = group.rows.flatMap(row => [row, ...(row.children ?? [])]).filter(rowVisible); if (!rows.length) return null;
      return <View key={index}>{group.title && <SectionTitle>{t(group.title)}</SectionTitle>}<LibraryGroup style={group.title ? undefined : {marginTop: 12}}>{rows.map(renderRow)}</LibraryGroup></View>;
    })}
    {error && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record, marginTop: 12}]}>{error}</Text>}
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open) setEditing(undefined);}} title={editing ? t(editing.label) : ''}
      footer={<Button disabled={busy || number === undefined} onPress={() => {if (editing && number !== undefined) {change(editing, number); setEditing(undefined);}}}>{t('action.save')}</Button>}>
      <TextField value={text} onChangeText={setText} keyboardType="number-pad" label={editing ? t(editing.label) : ''} description={editing?.control.unit ? t(editing.control.unit) : undefined}/>
    </BottomSheet>
  </>;
}
