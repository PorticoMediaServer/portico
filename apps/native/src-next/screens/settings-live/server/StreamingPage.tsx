import {OwnerStreamingSettings} from '../../../../../../packages/client-core/src/server-admin/streaming-settings';
import {withRequestRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {type SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {readOwnerStreamingHardware, runOwnerHardwareTest, type OwnerStreamingHardware} from '../../../../../../packages/client-core/src/server-admin/streaming-hardware';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {SettingsRow} from '../../../kit/shell';
import {FormSet} from '../../../../../../packages/client-core/src/server-admin/draft-form';
import {withFormValue} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {readSettingsBinding, settingsBinding, settingsBindingPatch, type SettingsBindingSources} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button} from '../../../kit/controls';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {NativeStreamingHardware} from './StreamingHardware';
import {settingEnabled, settingsSummary} from './settings-values';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {SettingsPageFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {hardwareSelectionChoices} from '../../../../../../packages/client-core/src/presentation/hardware-selection';
/** Phone Streaming uses the rebuilt documents through the shared row bindings and save path. */
/** On the Settings page frame (header, Back, server strip, gutters, scroll), with its Save bar under the frame's scroll.
 * `notice` is the screen's own error, shown first. */
export function NativeServerStreaming({ports, runtime, back, notice}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; back?: Readonly<{label: string; onPress: () => void}>; notice?: React.ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics(), t = runtime.text.t, context = ports.context;
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
  useEffect(() => {const read = new AbortController(); void owned.streaming.readConfiguration(read.signal).then(value => {if (!read.signal.aborted) setConfiguration(value);}, error => {if (!read.signal.aborted) setConfigurationError(error);}); return () => read.abort();}, [owned]);
  useEffect(() => {for (const form of Object.values(owned.forms)) void form.load(); return () => {for (const form of Object.values(owned.forms)) form.dispose(); owned.set.discard(); owned.lifetime.dispose();};}, [owned]);
  const runtimeState = useSyncExternalStore(owned.forms.runtime.subscribe, owned.forms.runtime.getSnapshot);
  const connectivityState = useSyncExternalStore(owned.forms.connectivity.subscribe, owned.forms.connectivity.getSnapshot);
  const state = useSyncExternalStore(owned.set.subscribe, owned.set.getSnapshot);
  const [discardRevision, setDiscardRevision] = useState(0), [advanced, setAdvanced] = useState(false), [customAway, setCustomAway] = useState(false);
  useEffect(() => setCustomAway(false), [connectivityState.saved]);
  const [invalid, setInvalid] = useState<ReadonlySet<string>>(new Set());
  const sources: SettingsBindingSources = {forms: {runtime: runtimeState.draft, connectivity: connectivityState.draft}};
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'streaming');
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
  const renderRow = (row: SpecRow): React.ReactNode => {
    const value = fact(row);
    if (row.id === 'streaming.hardware' && value) {
      // The adapters come from the hardware report, by their opaque ids; a saved adapter that
      // is gone stays drawn as the current choice.
      const choices = hardwareSelectionChoices(hardware?.devices ?? [], value.current, t);
      const options = choices.map(choice => ({value: choice.value, label: choice.label as never, locked: choice.locked, ...(choice.hint ? {hint: choice.hint as never} : {})}));
      // The choices are already in words (an adapter's name is the driver's own), so only the
      // row's own copy goes through the catalogue.
      const words = new Set(choices.flatMap(choice => [choice.label, choice.hint ?? '']));
      const translate = ((id: string) => words.has(id) ? id : t(id as never)) as typeof t;
      return <React.Fragment key={row.id}><NativeSettingControl key={`${row.id}:${discardRevision}`} row={{...row, control: {...row.control, options}}} value={value.current} t={translate}
        disabled={state.saving || !settingEnabled(row, read)} onChange={next => change(row, next)}/>
        {row.children?.map(renderRow)}</React.Fragment>;
    }
    // A drawn parent the page cannot paint (it has no binding of its own) still carries
    // dependent rows that do: paint those instead of hiding the whole branch.
    if (!value) return row.children?.length ? <React.Fragment key={row.id}>{row.children.map(renderRow)}</React.Fragment> : null;
    const shown = row.id === 'streaming.awayQuality' && customAway ? 'custom' : row.id === 'streaming.maxHardwareConversions' && value.current === 'unlimited' ? 'auto' : value.current;
    return <React.Fragment key={row.id}><NativeSettingControl key={`${row.id}:${discardRevision}`} row={row} value={shown} t={t} api={owned.api}
      disabled={state.saving || !settingEnabled(row, read)} onChange={next => {change(row, next); if (row.id === 'streaming.awayQuality') setCustomAway(false);}}
      onCustom={row.id === 'streaming.awayQuality' ? () => {setCustomAway(true); setAdvanced(true);} : undefined}
      onValidity={valid => setInvalid(before => {const next = new Set(before); if (valid) next.delete(row.id); else next.add(row.id); return next;})}/>
      {row.children?.map(renderRow)}</React.Fragment>;
  };
  const renderGroups = (groups: NonNullable<typeof section>['groups']) => groups?.map((group, index) => {const rows = group.rows.map(renderRow).filter(Boolean); return rows.length ? <View key={index} style={{gap: 10}}>{group.title && <Text style={[theme.type.rowHeading, {color: theme.colors.textSecondary}]}>{t(group.title)}</Text>}<View style={{backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14, gap: 16}}>{rows}</View></View> : null;});
  return <View style={{flex: 1}}><SettingsPageFrame title={section ? t(section.title) : t('settings.server.playback')} back={back}
    server={<ServerHeader serverName={context.serverName} viewerRole="owner" audience="owner" variant={frame.form === 'phone' ? 'phone' : 'page'}/>}>
    {notice}<View style={{gap: 18}}>
    {configurationError !== undefined && <StateView variant="inline" {...runtime.error(configurationError, () => {void owned.streaming.readConfiguration().then(value => {setConfiguration(value); setConfigurationError(undefined);}, setConfigurationError);}, 'server-console')}/>}
    {[runtimeState, connectivityState].map((form, index) => form.loadError !== undefined ? <StateView key={index} variant="inline" {...runtime.error(form.loadError, () => {void Object.values(owned.forms)[index]!.load();}, 'server-console')}/> : null)}
    {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, () => {if (!invalid.size && owned.lifetime.current()) void owned.set.save();}, 'server-console')}/>}
    {state.problem && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{t(state.problem as Parameters<typeof t>[0])}</Text>}
    {renderGroups(section?.groups)}
    {hardwareError !== undefined && <StateView variant="inline" {...runtime.error(hardwareError, loadHardware, 'server-console')}/>}
    {hardware && <NativeStreamingHardware hardware={hardware} testing={testing} tested={tested} disabled={testing || state.saving} onTest={() => {void runHardwareTest();}} t={t}/>}
    <NativeAdvancedSection testID="server-streaming-advanced" open={advanced} onOpenChange={setAdvanced} summary={!configuration || runtimeState.loading || connectivityState.loading ? t('status.loading') : settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], fact, t)}>{renderGroups(section?.advanced?.groups)}</NativeAdvancedSection>
  </View></SettingsPageFrame><View style={{flexDirection: 'row', justifyContent: 'flex-end', padding: 16, gap: 12}}><Button variant="glass" disabled={(!state.dirty && invalid.size === 0) || state.saving} onPress={() => {owned.set.discard(); setInvalid(new Set()); setCustomAway(false); setDiscardRevision(value => value + 1);}}>{t('action.discard')}</Button><Button disabled={!state.dirty || state.saving || invalid.size > 0 || !!state.problem} loading={state.saving} onPress={() => {if (!invalid.size && owned.lifetime.current()) void owned.set.save();}}>{t('action.save')}</Button></View></View>;
}

