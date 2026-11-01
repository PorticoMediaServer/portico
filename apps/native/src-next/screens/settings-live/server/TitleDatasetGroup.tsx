import React from 'react';
import {Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Switch} from '../../../kit/controls/Switch';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {LibraryGroup, LibraryRow, SectionTitle} from '../owner-libraries/controls';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {TVSwitchRow} from './TVSettingRows';

const IDS = ['runtime'] as const;
const ROW = 'metadata.titleDataset';

/** Settings › Metadata › Recommendations: the title dataset switch (owner runtime setting
 * recommendations.titleDataset). Like the page's other switches, a change saves at once. */
export function NativeTitleDatasetGroup({ports, runtime, tv = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; tv?: boolean}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <Group key={group.mount} group={group} runtime={runtime} tv={tv}/> : null;
}

function Group({group, runtime, tv}: {group: StorageForms; runtime: NativeShellRuntime; tv: boolean}) {
  const theme = useTheme(), t = runtime.text.t, form = group.forms.runtime;
  const state = useFormState<RuntimeSettings, unknown>(form), save = useStorageSet(group);
  const location = settingsRowIndex.get(ROW);
  if (!location || !state.draft) return null;
  const row = location.row, title = location.section.groups?.find(candidate => candidate.rows.some(value => value.id === ROW))?.title;
  const on = state.draft['recommendations.titleDataset'] !== false;
  const change = (next: boolean) => {
    if (save.saving || form.getSnapshot().saving) return;
    form.set((draft: RuntimeSettings) => ({...draft, 'recommendations.titleDataset': next}));
    void group.set.save();
  };
  const label = t(row.label), help = row.help ? t(row.help) : undefined;
  // TV draws the Metadata panel's own group: a heading over one raised card of focusable rows.
  if (tv) return <View style={{marginBottom: 30}}>
    {title && <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginLeft: 24, marginBottom: 10}]}>{t(title)}</Text>}
    <View style={{backgroundColor: theme.colors.raised, borderRadius: 18}}>
      <TVSwitchRow testID={ROW} label={label} description={help} checked={on} disabled={save.saving} onChange={change}/>
    </View>
  </View>;
  return <>
    {title && <SectionTitle>{t(title)}</SectionTitle>}
    <LibraryGroup>
      <LibraryRow first label={label} help={help}
        trailing={<Switch testID={ROW} accessibilityLabel={label} checked={on} disabled={save.saving} onCheckedChange={change}/>}/>
    </LibraryGroup>
  </>;
}
