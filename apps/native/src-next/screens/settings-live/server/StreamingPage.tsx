import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {parseSettingsConfiguration, type SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {nativeRouteCrypto} from '../../../platform/server-connections';
import {testHardware} from '../../../../../../packages/client-core/src/hardware';
import type {TranscodeCapacity} from '../../../../../../packages/client-core/src/telemetry';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {SettingsRow} from '../../../kit/shell';
import {ConsoleClient} from '../../../../../../packages/client-core/src/console';
import {FormSet} from '../../../../../../packages/client-core/src/server-admin/draft-form';
import {createServerForm, withFormValue} from '../../../../../../packages/client-core/src/server-admin/server-forms';
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
import {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {settingsPageHeading, settingsPageInset} from './page-layout';
import {hardwareSelectionChoices} from '../../../../../../packages/client-core/src/presentation/hardware-selection';
import {transcodeLocationChoice} from '../../../../../../packages/client-core/src/presentation/transcode-location';

/** Phone Streaming borrows checked revisioned runtime/connectivity documents, never delivery policy state. */
export function NativeServerStreaming({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const theme = useTheme(), t = runtime.text.t, context = ports.context;
  const owned = useMemo(() => {
    const lifetime = new NativeScreenLifetime(context);
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
    const console = new ConsoleClient(api, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId});
    const deps = {api, console, serverId: lifetime.authority.serverId, operationId: () => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('')};
    const forms = {runtime: createServerForm('runtime', deps), connectivity: createServerForm('connectivity', deps)};
    const set = new FormSet(); set.add('runtime', forms.runtime); set.add('connectivity', forms.connectivity);
    return {lifetime, api, console, forms, set};
  }, [context]);
  const capacityAlive = useRef(true), capacityPending = useRef(false);
  const [capacity, setCapacity] = useState<TranscodeCapacity>(), [capacityError, setCapacityError] = useState<unknown>(), [testing, setTesting] = useState(false);
  const runHardwareTest = async () => {
    if (capacityPending.current || !owned.lifetime.current()) return;
    capacityPending.current = true; setTesting(true);
    try {
      const value = await testHardware(owned.api);
      if (capacityAlive.current && owned.lifetime.current()) {setCapacity(value); setCapacityError(undefined);}
    } catch (error) {if (capacityAlive.current && owned.lifetime.current()) setCapacityError(error);}
    finally {capacityPending.current = false; if (capacityAlive.current && owned.lifetime.current()) setTesting(false);}
  };
  const loadCapacity = useCallback(() => {
    if (capacityPending.current || !owned.lifetime.current()) return;
    capacityPending.current = true;
    void owned.console.transcodeCapacity().then(value => {if (capacityAlive.current && owned.lifetime.current()) {setCapacity(value); setCapacityError(undefined);}}, error => {if (capacityAlive.current && owned.lifetime.current()) setCapacityError(error);}).finally(() => {capacityPending.current = false;});
  }, [owned]);
  // Capacity can probe FFmpeg on a cache miss: one page read, never a polling loop or a test result.
  useEffect(() => {capacityAlive.current = true; loadCapacity(); return () => {capacityAlive.current = false;};}, [loadCapacity]);
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [configurationError, setConfigurationError] = useState<unknown>();
  useEffect(() => {const read = new AbortController(); void owned.api.request(routePath('get_settings_configuration'), 'GET', undefined, read.signal).then(parseSettingsConfiguration).then(value => {if (!read.signal.aborted) setConfiguration(value);}, error => {if (!read.signal.aborted) setConfigurationError(error);}); return () => read.abort();}, [owned]);
  useEffect(() => {for (const form of Object.values(owned.forms)) void form.load(); return () => {for (const form of Object.values(owned.forms)) form.dispose(); owned.console.dispose(); owned.set.discard(); owned.lifetime.dispose();};}, [owned]);
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
    if (row.id === 'streaming.transcodeLocation') {
      const value = fact(row);
      if (!value) return null;
      const located = transcodeLocationChoice(capacity?.temporaryDirectory, typeof value.current === 'string' ? value.current : undefined);
      return <React.Fragment key={row.id}><NativeSettingControl key={`${row.id}:${discardRevision}`} row={{...row, help: located.memoryAvailable ? row.help : 'next.settings.streaming.memoryUnavailable'}} value={located.value} t={t}
        disabled={state.saving} onChange={next => {
          if (next === 'memory' || next === 'disk') change(row, next);
        }}/>
        {row.children?.map(renderRow)}</React.Fragment>;
    }
    const value = fact(row);
    if (row.id === 'streaming.hardware' && value) {
      // The adapters come from the hardware report, by their opaque ids; a saved adapter that
      // is gone stays drawn as the current choice.
      const choices = hardwareSelectionChoices(capacity?.hardware.devices ?? [], value.current, t);
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
    // The folder row is shown only under the Location choice above, whose value has no binding
    // of its own for the generic dependency check.
    const folderRow = row.id === 'streaming.transcodeFolder';
    const shown = row.id === 'streaming.awayQuality' && customAway ? 'custom' : row.id === 'streaming.maxHardwareConversions' && value.current === 'unlimited' ? 'auto' : value.current;
    return <React.Fragment key={row.id}><NativeSettingControl key={`${row.id}:${discardRevision}`} row={row} value={shown} t={t} api={owned.api}
      disabled={state.saving || !settingEnabled(row, read) && !folderRow} onChange={next => {change(row, next); if (row.id === 'streaming.awayQuality') setCustomAway(false);}}
      onCustom={row.id === 'streaming.awayQuality' ? () => {setCustomAway(true); setAdvanced(true);} : undefined}
      onValidity={valid => setInvalid(before => {const next = new Set(before); if (valid) next.delete(row.id); else next.add(row.id); return next;})}/>
      {row.children?.map(renderRow)}</React.Fragment>;
  };
  const renderGroups = (groups: NonNullable<typeof section>['groups']) => groups?.map((group, index) => {const rows = group.rows.map(renderRow).filter(Boolean); return rows.length ? <View key={index} style={{gap: 10}}>{group.title && <Text style={[theme.type.rowHeading, {color: theme.colors.textSecondary}]}>{t(group.title)}</Text>}<View style={{backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14, gap: 16}}>{rows}</View></View> : null;});
  return <View style={{flex: 1}}><ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader), gap: 18}}>
    <ServerHeader serverName={context.serverName} viewerRole="owner" audience="owner" variant="phone"/>
    {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{section ? t(section.title) : t('settings.server.playback')}</Text>}
    {configurationError !== undefined && <StateView variant="inline" {...runtime.error(configurationError, () => {void owned.api.request(routePath('get_settings_configuration')).then(parseSettingsConfiguration).then(value => {setConfiguration(value); setConfigurationError(undefined);}, setConfigurationError);}, 'server-console')}/>}
    {[runtimeState, connectivityState].map((form, index) => form.loadError !== undefined ? <StateView key={index} variant="inline" {...runtime.error(form.loadError, () => {void Object.values(owned.forms)[index]!.load();}, 'server-console')}/> : null)}
    {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, () => {if (!invalid.size && owned.lifetime.current()) void owned.set.save();}, 'server-console')}/>}
    {state.problem && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{t(state.problem as Parameters<typeof t>[0])}</Text>}
    {renderGroups(section?.groups)}
    {capacityError !== undefined && <StateView variant="inline" {...runtime.error(capacityError, loadCapacity, 'server-console')}/>}
    {capacity && <>
      <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}><SettingsRow first label={t('next.settingsSpec.server.streaming.groups.2.rows.2.label')} value={capacity.temporaryDirectory.path} description={!capacity.temporaryDirectory.ready ? t('error.unavailable') : capacity.temporaryDirectory.freeBytes === null ? undefined : t('settings.server.health.free', {bytes: defaultI18n.bytes(capacity.temporaryDirectory.freeBytes)})}/></View>
      <NativeStreamingHardware capacity={capacity} testing={testing} disabled={testing || state.saving} onTest={() => {void runHardwareTest();}} t={t}/>
    </>}
    <NativeAdvancedSection testID="server-streaming-advanced" open={advanced} onOpenChange={setAdvanced} summary={!configuration || runtimeState.loading || connectivityState.loading ? t('status.loading') : settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], fact, t)}>{renderGroups(section?.advanced?.groups)}</NativeAdvancedSection>
  </ScrollView><View style={{flexDirection: 'row', justifyContent: 'flex-end', padding: 16, gap: 12}}><Button variant="glass" disabled={(!state.dirty && invalid.size === 0) || state.saving} onPress={() => {owned.set.discard(); setInvalid(new Set()); setCustomAway(false); setDiscardRevision(value => value + 1);}}>{t('action.discard')}</Button><Button disabled={!state.dirty || state.saving || invalid.size > 0 || !!state.problem} loading={state.saving} onPress={() => {if (!invalid.size && owned.lifetime.current()) void owned.set.save();}}>{t('action.save')}</Button></View></View>;
}

