import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {PreferenceValue} from '../../../../../../packages/client-core/src/preferences';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {nativeReadingSettingsSection, type NativeReadingSettingsPreferences} from '../../../screens/settings/reading-preferences';

export function useReadingSettings({source, runtime, current}: {
  source: NativeReadingSettingsPreferences; runtime: NativeShellRuntime; current: () => boolean;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getState), values = source.getSnapshot();
  const [advanced, setAdvanced] = useState(false);
  const currentRoute = useRef(current); currentRoute.current = current;
  useEffect(() => {if (currentRoute.current()) void source.refresh();}, [source]);
  const change = (row: SettingsRow, value: PreferenceValue | null) => {
    if (!current() || state.busy) return;
    void source.setRow(row.id, value).catch(error => {if (source.current() && source.getState().error === undefined) runtime.reportError(error);});
  };
  const groups = [...(nativeReadingSettingsSection.groups ?? []), ...(advanced ? nativeReadingSettingsSection.advanced?.groups ?? [] : [])];
  return {state, values, advanced, setAdvanced, change, groups};
}
