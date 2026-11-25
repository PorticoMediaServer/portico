import {defaultI18n} from '@i18n';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {hardwareSelectionChoices} from '@core/presentation/hardware-selection.ts';
import type {SettingsController} from '../controller';
import {createServerSettingsSurfaces} from '../server';
import {GeneralPause} from '../server/GeneralPause';
import {SettingsUpdatesSource} from '../server/updates-source';
import {UpdatesCheckNow, UpdatesCurrent} from '../server/GeneralUpdates';
import {GeneralMessage, GeneralRestart} from '../server/GeneralTools';
import {SettingsHardwareSource} from '../hardware/hardware-source';
import {HardwareDevices, TestHardware, TranscodeFolder} from '../hardware/HardwareRows';
import {hardwareEligible, hardwareRowState} from '../hardware/hardware-rows';
import {activeServerSection, type ServerSectionGateInput} from './access-gate';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

const deprecatedMissingRows = new Set(['storage.missingRule', 'storage.missingNow', 'storage.removeMissing', 'storage.missingDaysExact']);
const generalActionRows = new Set(['general.message', 'general.restart', 'general.pauseBackground']);

/** Server › General (updates, tools, server ID), Streaming (hardware), and Storage & backups. */
export function createServerGeneralStreamingSection(
  env: SettingsRouteEnv,
  controller: SettingsController,
  gate: ServerSectionGateInput,
): SettingsSectionSurface {
  const {api, route, viewer, viewerId, current, reportError, reportActionError, openSettingsRow} = env;
  // Server › Streaming › Hardware draws measured adapters, formats and probes: one owner read per visit.
  const hardware = activeServerSection(route, 'streaming', gate)
    ? new SettingsHardwareSource(api, () => current(), reportError) : undefined;
  if (hardware) void hardware.load();
  // Server › General › Updates: the cached report the page reads, plus one explicit feed check on press.
  const updates = activeServerSection(route, 'general', gate)
    ? new SettingsUpdatesSource(api, () => current(), reportError) : undefined;
  if (updates) void updates.load();
  // Server › General › Tools draws the owner actions over their real owner routes.
  const generalActions = activeServerSection(route, 'general', gate);
  // Storage & backups owns one bounded usage sweep shared by its drawn rows.
  const server = activeServerSection(route, 'storage', gate)
    ? createServerSettingsSurfaces({
      transport: api, serverId: viewer.serverId, isCurrent: current, openTarget: openSettingsRow,
      backups: controller.backups, subscribe: controller.subscribe, scopeKey: `${viewer.serverId}/${viewerId}`, reportError,
    }) : undefined;

  return {
    subscribe: listener => {
      const offHardware = hardware?.subscribe(listener), offServer = server?.subscribe(listener);
      return () => {offHardware?.(); offServer?.();};
    },
    getSnapshot: (section, source) => {
      if (section.scope === 'server' && section.id === 'storage' && server) return server.pageSnapshot(source.getSnapshot(section));
      if (section.scope === 'server' && section.id === 'streaming' && hardware) return hardware.pageSnapshot(source.getSnapshot(section));
      return undefined;
    },
    eligible: (row, selected, ctrl) => {
      if (hardware && hardwareEligible(row.id, hardware.getSnapshot())) return current() && ctrl.sectionVisible(selected.section);
      if (generalActionRows.has(row.id) || row.id === 'general.serverId') return generalActions && current() && ctrl.sectionVisible(selected.section);
      if (deprecatedMissingRows.has(row.id)) return false;
      const storageEligible = server?.eligible(row);
      if (storageEligible !== undefined) return storageEligible;
      return undefined;
    },
    rowState: (row, selected, ctrl, source) => {
      // Missing titles are each library's own now (03-scanning rules 60 and 61).
      if (deprecatedMissingRows.has(row.id)) return {present: false};
      if (generalActionRows.has(row.id)) return {present: generalActions && current() && ctrl.sectionVisible(selected.section)};
      // General › Advanced › Server ID: this server's id, read-only, for support.
      if (row.id === 'general.serverId') return {present: generalActions && current() && ctrl.sectionVisible(selected.section), displayValue: viewer.serverId, readOnly: true};
      if (row.id === 'streaming.hardware') {
        const state = source.rowState(row, selected), devices = hardware?.getSnapshot().hardware?.devices ?? [];
        return state.present ? {...state, choices: hardwareSelectionChoices(devices, state.value, defaultI18n.t)} : state;
      }
      const hardwareState = hardware ? hardwareRowState(row.id, hardware.getSnapshot()) : undefined;
      if (hardwareState !== undefined) return hardwareState;
      const storageState = server?.rowState(row);
      if (storageState !== undefined) return storageState;
      return undefined;
    },
    special: (row, state, rowContext, _openPanel, ctrl) => {
      if (row.id === 'general.version' && updates) return <UpdatesCurrent source={updates} />;
      if (row.id === 'general.checkNow' && updates) return <UpdatesCheckNow source={updates} label={defaultI18n.t(row.control.label ?? row.label)} disabled={state.disabled === true} />;
      if (row.id === 'general.pauseBackground' && generalActions) return <GeneralPause api={api} isCurrent={current} reportError={reportActionError} label={defaultI18n.t(row.control.label ?? row.label)} />;
      if (generalActionRows.has(row.id) && generalActions) {
        const label = defaultI18n.t(row.control.label ?? row.label);
        return row.id === 'general.message'
          ? <GeneralMessage api={api} isCurrent={current} reportError={reportActionError} disabled={state.disabled === true} label={label} />
          : <GeneralRestart api={api} isCurrent={current} reportError={reportActionError} disabled={state.disabled === true} label={label}
            confirm={row.control.confirm ? defaultI18n.t(row.control.confirm) : undefined} />;
      }
      if (row.id === 'streaming.transcodeFolder' && hardware) {
        return <TranscodeFolder label={defaultI18n.t(row.label)} value={typeof state.value === 'string' ? state.value : ''}
          disabled={state.disabled} api={api} onChange={path => {if (current()) ctrl.change(settingsRowIndex.get('streaming.transcodeFolder')!.row, path, rowContext);}} />;
      }
      if (row.id === 'streaming.hwDevices' && hardware) return <HardwareDevices source={hardware} />;
      if (row.id === 'streaming.hwTest' && hardware) return <TestHardware source={hardware} />;
      const storageControl = server?.special(row, state);
      if (storageControl !== undefined) return storageControl;
      return undefined;
    },
    dispose: () => {
      server?.dispose();
      updates?.dispose();
      hardware?.dispose();
    },
  };
}
