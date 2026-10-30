import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {MaintenanceDocument, MaintenanceSettings} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import type {DraftForm, FormState} from '../../../../../../packages/client-core/src/server-admin/draft-form';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {patchHeavyWork, readHeavyWork, type HeavyWorkRow} from './heavy-work';

/** The revisioned maintenance form owns saving; these rows only edit its heavy-work draft. */
export function HeavyWorkControls({form, state, t, placement = 'all', open, onOpenChange, customDraft: parentCustomDraft, onCustomDraftChange}: {
  form: DraftForm<MaintenanceSettings, MaintenanceDocument>;
  state: FormState<MaintenanceSettings, MaintenanceDocument>;
  t: typeof defaultI18n.t;
  placement?: 'all' | 'main' | 'advanced';
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  customDraft?: MaintenanceSettings;
  onCustomDraftChange?: (draft: MaintenanceSettings | undefined) => void;
}) {
  const theme = useTheme();
  const [advanced, setAdvanced] = useState(false), [customDraft, setCustomDraft] = useState<MaintenanceSettings>();
  const draft = state.draft;
  if (!draft) return null;
  const mode = readHeavyWork(draft, 'general.heavyWork');
  if (mode === undefined) return null;
  // Custom is an editing intent as well as a derived mode: enabling an existing
  // overnight-shaped window must allow its actual fields to be edited unchanged.
  const editingCustom = mode === 'custom' || (onCustomDraftChange ? parentCustomDraft : customDraft) === draft;
  const change = (id: HeavyWorkRow, value: unknown) => {
    form.set(current => patchHeavyWork(current, id, value));
    const next = id === 'general.heavyWork' ? value === 'custom' ? form.getSnapshot().draft : undefined : editingCustom ? form.getSnapshot().draft : undefined;
    setCustomDraft(next); onCustomDraftChange?.(next);
  };
  const select = (id: HeavyWorkRow, value: unknown, disabled = false) => {
    const row = settingsRowIndex.get(id)!.row;
    return <Select key={id} testID={id} label={t(row.label)} description={row.help ? t(row.help) : undefined}
      value={String(value)} disabled={disabled || state.saving} style={{marginTop: 12}}
      options={(row.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}))}
      onValueChange={next => {
        if (id === 'general.heavyWork' && next === 'custom') {setAdvanced(true); onOpenChange?.(true);}
        change(id, next);
      }}/>;
  };
  const dayRow = settingsRowIndex.get('general.heavyDays')!.row;
  const days = readHeavyWork(draft, 'general.heavyDays') as Readonly<Record<string, string>>;
  return <View>
    {placement !== 'advanced' && select('general.heavyWork', editingCustom ? 'custom' : mode)}
    {placement === 'all' && <Button testID="server-general-heavy-advanced" variant="glass" size="compact" style={{marginTop: 12}} trailingIcon={(open ?? advanced) ? 'i-up' : 'i-down'} onPress={() => {setAdvanced(!(open ?? advanced)); onOpenChange?.(!(open ?? advanced));}}>{t('settings.section.advanced')}</Button>}
    {placement !== 'main' && (placement === 'advanced' || (open ?? advanced)) && <>
      {select('general.heavyStart', readHeavyWork(draft, 'general.heavyStart'), !editingCustom)}
      {select('general.heavyLength', readHeavyWork(draft, 'general.heavyLength'), !editingCustom)}
      <Text style={[shellType(17, 800), {color: theme.colors.text, marginTop: 18}]}>{t(dayRow.label)}</Text>
      {(dayRow.control.items ?? []).map(day => <Select key={day.id} testID={`general.heavyDays.${day.id}`} rowLabel={t(day.label)} value={days[day.id!]}
        disabled={!editingCustom || state.saving} style={{marginTop: 6}}
        options={(dayRow.control.columns ?? []).map(column => ({value: column.value!, label: t(column.label)}))}
        onValueChange={value => change('general.heavyDays', {...days, [day.id!]: value})}/>) }
    </>}
  </View>;
}
