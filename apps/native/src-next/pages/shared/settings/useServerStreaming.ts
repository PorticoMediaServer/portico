import {OwnerStreamingSettings} from '../../../../../../packages/client-core/src/server-admin/streaming-settings';
import {withRequestRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {type SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {readOwnerStreamingHardware, runOwnerHardwareTest, type OwnerStreamingHardware} from '../../../../../../packages/client-core/src/server-admin/streaming-hardware';
import {FormSet} from '../../../../../../packages/client-core/src/server-admin/draft-form';
import {withFormValue} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {readSettingsBinding, settingsBinding, settingsBindingPatch, type SettingsBindingSources} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {settingEnabled, settingsSummary} from '../../../screens/settings-live/server/settings-values';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {hardwareSelectionChoices} from '../../../../../../packages/client-core/src/presentation/hardware-selection';

const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'streaming');

export function useServerStreaming({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {context} = ports, {t} = runtime.text;
  const owned = useMemo(() => {
    const lifetime = new NativeScreenLifetime(context);
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
    const streaming = new OwnerStreamingSettings(withRequestRaw(api, context.api.requestRaw?.bind(context.api), lifetime.assert));
    const forms = {runtime: streaming.form('runtime'), connectivity: streaming.form('connectivity')};
    const set = new FormSet(); set.add('runtime', forms.runtime); set.add('connectivity', forms.connectivity);
    return {lifetime, api, forms, set, streaming};
  }, [context]);
  // The rebuilt server's measured hardware report; Test hardware starts one bounded test and reads until it ends.
  const hardwareAlive = useRef(true), hardwarePending = useRef(false);
  const [hardware, setHardware] = useState<OwnerStreamingHardware>(), [hardwareError, setHardwareError] = useState<unknown>(), [testing, setTesting] = useState(false), [tested, setTested] = useState<'ok' | 'problem'>();
  const owns = () => hardwareAlive.current && owned.lifetime.current();
  const runHardwareTest = async () => {
    if (hardwarePending.current || !owned.lifetime.current()) return;
    hardwarePending.current = true; setTesting(true); setTested(undefined);
    try {
      const value = await runOwnerHardwareTest(owned.api, ms => new Promise(resolve => setTimeout(resolve, ms)));
      if (owns()) {setHardware(value); setHardwareError(undefined); setTested(value.checking ? undefined : value.lastError ? 'problem' : 'ok');}
    } catch (error) {if (owns()) setHardwareError(error);}
    finally {hardwarePending.current = false; if (owns()) setTesting(false);}
  };
  const loadHardware = useCallback(() => {
    if (hardwarePending.current || !owned.lifetime.current()) return;
    hardwarePending.current = true;
    void readOwnerStreamingHardware(owned.api).then(value => {if (owns()) {setHardware(value); setHardwareError(undefined);}}, error => {if (owns()) setHardwareError(error);}).finally(() => {hardwarePending.current = false;});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [owned]);
  useEffect(() => {hardwareAlive.current = true; loadHardware(); return () => {hardwareAlive.current = false;};}, [loadHardware]);
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [configurationError, setConfigurationError] = useState<unknown>();
  const loadConfiguration = useCallback((signal?: AbortSignal) => {
    void owned.streaming.readConfiguration(signal).then(value => {
      if (!signal?.aborted && owned.lifetime.current()) {setConfiguration(value); setConfigurationError(undefined);}
    }, error => {if (!signal?.aborted && owned.lifetime.current()) setConfigurationError(error);});
  }, [owned]);
  useEffect(() => {const read = new AbortController(); loadConfiguration(read.signal); return () => read.abort();}, [loadConfiguration]);
  useEffect(() => {for (const form of Object.values(owned.forms)) void form.load(); return () => {for (const form of Object.values(owned.forms)) form.dispose(); owned.set.discard(); owned.lifetime.dispose();};}, [owned]);
  const runtimeState = useSyncExternalStore(owned.forms.runtime.subscribe, owned.forms.runtime.getSnapshot);
  const connectivityState = useSyncExternalStore(owned.forms.connectivity.subscribe, owned.forms.connectivity.getSnapshot);
  const state = useSyncExternalStore(owned.set.subscribe, owned.set.getSnapshot);
  const [discardRevision, setDiscardRevision] = useState(0), [advanced, setAdvanced] = useState(false), [customAway, setCustomAway] = useState(false);
  useEffect(() => setCustomAway(false), [connectivityState.saved]);
  const [invalid, setInvalid] = useState<ReadonlySet<string>>(new Set());
  const sources: SettingsBindingSources = {forms: {runtime: runtimeState.draft, connectivity: connectivityState.draft}};
  const change = (row: SpecRow, value: unknown) => {
    if (!owned.lifetime.current() || owned.set.getSnapshot().saving) return; const binding = settingsBinding(row.id); if (!binding || !['runtime', 'connectivity'].includes(binding.form)) return;
    const patch = settingsBindingPatch(binding, value, sources), form = owned.forms[patch.form as keyof typeof owned.forms];
    if (!form) return;
    form.set((draft: Record<string, unknown>) => Object.entries(patch.values).reduce((next, [key, field]) => withFormValue(next, key, field), draft));
  };
  const fact = (row: SpecRow) => {
    const binding = settingsBinding(row.id); if (!binding || !['runtime', 'connectivity'].includes(binding.form)) return undefined;
    const policy = configuration?.fields.find(field => field.id === (row.id === 'streaming.awayQuality' ? 'streaming.awayExact' : row.id));
    if (!configuration || !policy || policy.configOnly) return undefined;
    return readSettingsBinding(binding, sources, row.id);
  };
  const read = (id: string): unknown => {
    if (id === 'streaming.awayQuality' && customAway) return 'custom';
    const binding = settingsBinding(id); return binding ? readSettingsBinding(binding, sources, id)?.current : undefined;
  };
  const field = (row: SpecRow) => {
    const value = fact(row);
    if (!value) return undefined;
    const disabled = state.saving || !settingEnabled(row, read), key = `${row.id}:${discardRevision}`;
    if (row.id === 'streaming.hardware') {
      // A saved adapter that is no longer measured stays available as the current choice.
      const choices = hardwareSelectionChoices(hardware?.devices ?? [], value.current, t);
      const options = choices.map(choice => ({value: choice.value, label: choice.label as never, locked: choice.locked, ...(choice.hint ? {hint: choice.hint as never} : {})}));
      const words = new Set(choices.flatMap(choice => [choice.label, choice.hint ?? '']));
      const translate = ((id: string) => words.has(id) ? id : t(id as never)) as typeof t;
      return {key, control: {row: {...row, control: {...row.control, options}}, value: value.current, t: translate,
        disabled, onChange: (next: unknown) => change(row, next)}};
    }
    const shown = row.id === 'streaming.awayQuality' && customAway ? 'custom' : row.id === 'streaming.maxHardwareConversions' && value.current === 'unlimited' ? 'auto' : value.current;
    return {key, control: {row, value: shown, t, api: owned.api, disabled,
      onChange: (next: unknown) => {change(row, next); if (row.id === 'streaming.awayQuality') setCustomAway(false);},
      onCustom: row.id === 'streaming.awayQuality' ? () => {setCustomAway(true); setAdvanced(true);} : undefined,
      onValidity: (valid: boolean) => setInvalid(before => {const next = new Set(before); if (valid) next.delete(row.id); else next.add(row.id); return next;}),
    }};
  };
  const save = () => {if (!invalid.size && owned.lifetime.current()) void owned.set.save();};
  return {
    t, section, field, configurationError, loadConfiguration, forms: Object.values(owned.forms),
    formStates: [runtimeState, connectivityState], hardware, hardwareError, loadHardware, runHardwareTest, testing, tested,
    state, advanced, setAdvanced, save,
    advancedSummary: !configuration || runtimeState.loading || connectivityState.loading ? t('status.loading')
      : settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], fact, t),
    canDiscard: (state.dirty || invalid.size > 0) && !state.saving,
    canSave: state.dirty && !state.saving && invalid.size === 0 && !state.problem,
    discard: () => {owned.set.discard(); setInvalid(new Set()); setCustomAway(false); setDiscardRevision(value => value + 1);},
  };
}
