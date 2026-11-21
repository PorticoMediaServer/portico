import {useLayoutEffect, useRef, useState} from 'react';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {remeasureAllSegments, retryUnreadableSegments} from '../../../../../../packages/client-core/src/segment-review';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {settingEnabled, settingsSummary} from '../../../screens/settings-live/server/settings-values';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from '../../../screens/settings-live/server/storage/forms';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';

const ids = ['segments'] as const;
const bulkActions = new Set(['segments.redetectAll', 'segments.retryFailed']);
const sections = settingsSpec.find(section => section.scope === 'server' && section.id === 'segments')?.advanced?.groups ?? [];

export function useSegmentsForms(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  return useStorageForms(ports, ids, runtime.reportError);
}

export function useServerSegments(group: StorageForms, runtime: NativeShellRuntime) {
  const {forms, set, context} = group, {t} = runtime.text;
  const state = useStorageSet(group), settings = useFormState<Record<string, unknown>, unknown>(forms.segments);
  const [invalid, setInvalid] = useState<ReadonlySet<string>>(new Set()), [draftKey, setDraftKey] = useState(0);
  const sources = {forms: {segments: settings.draft}};
  const current = () => nativeStorageOwner(context) && !set.getSnapshot().saving;
  const read = (row: SettingsRow) => {
    const binding = settingsBinding(row.id);
    return binding?.form === 'segments' ? readSettingsBinding(binding, sources, row.id) : undefined;
  };
  const controls = (rows: readonly SettingsRow[]) => rows.flatMap<{row: SettingsRow; value?: unknown}>(row => {
    if (bulkActions.has(row.id)) return nativeStorageOwner(context) ? [{row}] : [];
    const value = read(row)?.current;
    if (value === undefined) return [];
    return [{row, value}];
  });
  const rowProps = (row: SettingsRow, value: unknown) => ({
    row, value, t,
    disabled: state.saving || !settingEnabled(row, id => {
      const other = settingsRowIndex.get(id)?.row;
      return other ? read(other)?.current : undefined;
    }),
    onValidity: (valid: boolean) => setInvalid(previous => {
      if (previous.has(row.id) === !valid) return previous;
      const next = new Set(previous);
      if (valid) next.delete(row.id); else next.add(row.id);
      return next;
    }),
    onChange: (next: unknown) => {
      if (!current()) return;
      const patch = settingsBindingPatch(settingsBinding(row.id)!, next, sources);
      forms.segments.set((draft: Record<string, unknown>) => applySettingsBindingPatch(draft, patch));
    },
  });
  const mode = settingsRowIndex.get('segments.mode')!.row;
  return {
    state, settings, draftKey, mode: settings.draft ? rowProps(mode, read(mode)?.current) : undefined, rowProps,
    groups: sections.map(group => ({title: group.title, controls: controls(group.rows)})).filter(group => group.controls.length),
    summary: settingsSummary(sections.flatMap(group => group.rows), read, t), canSave: !state.problem && !invalid.size,
    retry: () => {void forms.segments.load();},
    save: () => {if (current() && !invalid.size) void set.save();},
    discard: () => {if (current()) {set.discard(); setInvalid(new Set()); setDraftKey(key => key + 1);}},
  };
}

export function useSegmentsBulkAction(context: NativeShellBootstrapContext, row: SettingsRow) {
  const flight = useRef<NativeScreenLifetime | undefined>(undefined);
  const [lifetime, setLifetime] = useState<NativeScreenLifetime>();
  const [asking, setAsking] = useState(false), [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false), [done, setDone] = useState(false);
  useLayoutEffect(() => {
    const next = new NativeScreenLifetime(context);
    setLifetime(next);
    return () => next.dispose();
  }, [context]);
  const current = () => lifetime?.current() && nativeStorageOwner(context);
  const run = async () => {
    if (!lifetime || !current() || flight.current === lifetime) return;
    flight.current = lifetime;
    setBusy(true); setFailed(false);
    const command = row.id === 'segments.redetectAll' ? remeasureAllSegments : retryUnreadableSegments;
    try {
      await lifetime.request(signal => command(context.api, signal));
      if (current()) {setAsking(false); setDone(true);}
    } catch {if (current()) setFailed(true);}
    finally {
      if (flight.current === lifetime) flight.current = undefined;
      if (current()) setBusy(false);
    }
  };
  return {
    asking, busy, failed, done, run,
    open: () => {if (row.control.confirm) setAsking(true); else void run();},
    close: () => {if (!busy) setAsking(false);},
  };
}
