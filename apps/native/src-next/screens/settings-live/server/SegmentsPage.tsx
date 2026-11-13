import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {StateView} from '../../../kit/states';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {settingEnabled, settingsSummary} from './settings-values';
import {nativeStorageOwner} from './storage/space-source';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {remeasureAllSegments, retryUnreadableSegments, type SegmentApi} from '../../../../../../packages/client-core/src/segment-review';
import {BottomSheet} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';

// Intros and credits' settings document on the rebuilt server (#3255).
const IDS = ['segments'] as const;
/** The spec's two bulk actions (#3255, client wrappers #3308): owner-only, drawn on every platform (no platforms tag). */
const BULK: ReadonlySet<string> = new Set(['segments.redetectAll', 'segments.retryFailed']);

/** Server settings rows use revisioned forms, never the account preference renderer. */
export function NativeServerSegments({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <Segments key={group.mount} group={group} ports={ports} runtime={runtime}/> : null;
}
function Segments({ports, runtime, group}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; group: StorageForms}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const {forms, set} = group, state = useStorageSet(group);
  const settings = useFormState<Record<string, unknown>, unknown>(forms.segments);
  const [invalid, setInvalid] = useState<ReadonlySet<string>>(new Set()), [draftKey, setDraftKey] = useState(0);
  const sources = {forms: {segments: settings.draft}};
  const read = (row: SpecRow) => {const binding = settingsBinding(row.id);return binding?.form === 'segments' ? readSettingsBinding(binding, sources, row.id) : undefined;};
  const current = () => nativeStorageOwner(group.context) && !set.getSnapshot().saving;
  const renderRow = (row: SpecRow) => {
    if (BULK.has(row.id)) return nativeStorageOwner(group.context) ? <SegmentsBulkAction key={row.id} row={row} api={group.context.api} t={t}/> : null;
    const value = read(row)?.current;
    if (value === undefined) return null;
    return <NativeSettingControl key={`${row.id}:${draftKey}`} row={row} value={value} t={t}
      disabled={state.saving || !settingEnabled(row, id => {const other = settingsRowIndex.get(id)?.row;return other ? read(other)?.current : undefined;})}
      onValidity={valid => setInvalid(previous => {const next = new Set(previous);if (valid) next.delete(row.id);else next.add(row.id);return next;})}
      onChange={next => {if (!current()) return;const patch = settingsBindingPatch(settingsBinding(row.id)!, next, sources);forms.segments.set((draft: Record<string, unknown>) => applySettingsBindingPatch(draft, patch));}}/>;
  };
  const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'segments');
  const save = () => {if (current() && !invalid.size) void set.save();};
  if (settings.loadError !== undefined && !settings.draft) return <StateView {...runtime.error(settings.loadError, () => {void forms.segments.load();}, 'server-console')}/>;
  return <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 32}}>
    {settings.draft && renderRow(settingsRowIndex.get('segments.mode')!.row)}
    {ports.renderSettingsPanel('server.segments', runtime)}
    <NativeAdvancedSection testID="server-segments-advanced" summary={settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], read, t)}>
      {section?.advanced?.groups?.map((group, index) => {
        const rows = group.rows.map(renderRow).filter(Boolean);
        return rows.length ? <View key={index} style={{gap: 10}}>{group.title && <Text style={[shellType(16, 700), {color: theme.colors.textSecondary}]}>{t(group.title)}</Text>}{rows}</View> : null;
      })}
    </NativeAdvancedSection>
    {state.dirty && <View style={{flexDirection: 'row', gap: 8, marginTop: 12}}>
      <Button variant="glass" disabled={state.saving} onPress={() => {if (current()) {set.discard();setInvalid(new Set());setDraftKey(key => key + 1);}}}>{t('action.discard')}</Button>
      <Button loading={state.saving} disabled={Boolean(state.problem) || invalid.size > 0} onPress={save}>{t('action.save')}</Button>
    </View>}
    {state.problem && <StateView title={t(state.problem as MessageId)} tone="error"/>}
    {settings.saveError !== undefined && <StateView {...runtime.error(settings.saveError, save, 'server-console')}/>}
  </ScrollView>;
}

/** Measure everything again (after the spec's confirmation) or try the unreadable files again. */
function SegmentsBulkAction({row, api, t}: {row: SpecRow; api: SegmentApi; t: typeof defaultI18n.t}) {
  const theme = useTheme(), [asking, setAsking] = useState(false), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false), [done, setDone] = useState(false);
  const label = t((row.control.label ?? row.label) as MessageId);
  const run = async () => {
    setBusy(true); setFailed(false);
    try {await (row.id === 'segments.redetectAll' ? remeasureAllSegments(api) : retryUnreadableSegments(api)); setAsking(false); setDone(true);}
    catch {setFailed(true);}
    finally {setBusy(false);}
  };
  return <View>
    <SettingsRow first label={t(row.label as MessageId)} description={row.help ? t(row.help as MessageId) : undefined}
      control={<Button size="compact" variant="glass" loading={busy && !asking} disabled={busy} onPress={() => {if (row.control.confirm) setAsking(true); else void run();}}>{label}</Button>}/>
    {failed && !asking ? <Text accessibilityRole="alert" style={[shellType(13, 500), {color: theme.colors.record, marginTop: 4}]}>{t('next.states.pageFailedBody')}</Text> : null}
    {done && !failed ? <Text style={[shellType(13, 500), {color: theme.colors.textTertiary, marginTop: 4}]}>{t('web.segments.bulkStarted')}</Text> : null}
    {row.control.confirm ? <BottomSheet open={asking} onOpenChange={open => {if (!open && !busy) setAsking(false);}} title={label} description={t(row.control.confirm as MessageId)} fitContent
      footer={<View style={{gap: 10}}><Button loading={busy} disabled={busy} onPress={() => {void run();}}>{label}</Button><Button variant="glass" disabled={busy} onPress={() => setAsking(false)}>{t('next.settings.cancel')}</Button></View>}>
      {failed ? <Text accessibilityRole="alert" style={[shellType(13, 500), {color: theme.colors.record}]}>{t('next.states.pageFailedBody')}</Text> : null}
    </BottomSheet> : null}
  </View>;
}
