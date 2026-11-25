import {defaultI18n} from '@i18n';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {OwnerRemoteSettings} from '@core/server-admin/remote-settings.ts';
import {Button} from '../../../kit/controls';
import {SettingsStringList} from '../../../kit/settings/SettingsStringList';
import type {SettingsPageSnapshot} from '../Settings';
import {RemoteSourcesPage} from '../remote-sources/RemoteSourcesPage';
import {OwnerRemotePage, OwnAddressControl, OwnerRefusedAddresses, OwnerBlockedNetworks} from '../server-remote/OwnerRemotePage';
import {MetadataSettingsPage} from '../metadata/MetadataSettingsPage';
import {activeServerSection, canManageServerSection, type ServerSectionGateInput} from './access-gate';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

/** Stable snapshot for self-loading owner pages that never enter the generic section loader. */
export const selfLoadedPageSnapshot: SettingsPageSnapshot = Object.freeze({loading: false});

const remoteFactRows = new Set(['remote.refused', 'remote.blocked', 'remote.thisDevice', 'remote.listen', 'remote.addresses']);
const ownAddressChildren = new Set(['remote.externalAddress', 'remote.certFile', 'remote.certKey', 'remote.certStatus', 'remote.identityCode']);
const remoteStringListRows = new Set(['remote.homeNets', 'remote.customAddresses', 'remote.trustedProxies']);

export type ServerRemoteMetadataHandle = Readonly<{
  surface: SettingsSectionSurface;
  remoteSettings?: OwnerRemoteSettings;
}>;

/** Server › Remote access, Server › Metadata, and Server › Remote sources. */
export function createServerRemoteMetadataSection(
  env: SettingsRouteEnv,
  gate: ServerSectionGateInput,
): ServerRemoteMetadataHandle {
  const {api, route, viewer, viewerId, current, reportError, reportActionError, openSettings, openSettingsRow} = env;
  const metadataCurrent = () => current() && canManageServerSection('metadata', gate);
  const remoteSettings = activeServerSection(route, 'remote', gate)
    ? new OwnerRemoteSettings(api, current) : undefined;
  let remotePage: {rows?: unknown; remote?: unknown; value?: SettingsPageSnapshot} = {};
  const remotePageSnapshot = (rows: SettingsPageSnapshot) => {
    const remote = remoteSettings?.getSnapshot();
    if (remotePage.rows !== rows || remotePage.remote !== remote) remotePage = {rows, remote, value: {...rows}};
    return remotePage.value!;
  };

  const surface: SettingsSectionSurface = {
    hasEngine: row => !!remoteSettings && !ownAddressChildren.has(row.id) && (remoteSettings.has(row.id) || remoteFactRows.has(row.id)) ? true : undefined,
    subscribe: listener => remoteSettings?.subscribe(listener) ?? (() => {}),
    getSnapshot: (section, source) => {
      if (section.scope === 'server' && section.id === 'remote' && remoteSettings) return remotePageSnapshot(source.getSnapshot(section));
      if (section.scope === 'server' && (section.id === 'remote-sources' || section.id === 'metadata' && metadataCurrent())) return selfLoadedPageSnapshot;
      return undefined;
    },
    load: (section, _source, controller) => section.scope === 'server' && section.id === 'remote-sources' ? controller.loadServerHeader(section) : undefined,
    eligible: (row, selected, controller) => {
      if (remoteSettings?.has(row.id) || remoteFactRows.has(row.id)) {
        return !!remoteSettings && !ownAddressChildren.has(row.id) && current()
          && selected.section.scope === 'server' && selected.section.id === 'remote' && controller.sectionVisible(selected.section);
      }
      if (row.id === 'metadata.perLibrary') return current() && controller.sectionVisible(selected.section);
      return undefined;
    },
    rowState: (row, selected, controller, source) => {
      if (remoteSettings) {
        if (ownAddressChildren.has(row.id)) return {present: false};
        const remote = remoteSettings.getSnapshot(), status = remote.status;
        const disabled = remote.busy || !!remote.confirmation || !current();
        if (row.id === 'remote.ownCertificate') return {present: true, bare: true, control: <OwnAddressControl key={remote.interfaces?.etag} source={remoteSettings} />};
        if (row.id === 'remote.refused') {
          return {
            present: true, wide: true,
            control: <OwnerRefusedAddresses source={remoteSettings}
              onStandard={() => source.change(settingsRowIndex.get('remote.security')!.row, 'standard', selected)}
              onCertificate={() => openSettingsRow({scope: 'server', section: 'remote', row: 'remote.ownCertificate'})} />,
          };
        }
        if (row.id === 'remote.blocked') return {present: true, wide: true, control: <OwnerBlockedNetworks source={remoteSettings} />};
        if (row.id === 'remote.thisDevice') {
          return {present: !!status, displayValue: status && defaultI18n.t(status.thisDevice.atHome ? 'next.remoteStatus.deviceHome' : 'next.remoteStatus.deviceAway', {address: status.thisDevice.address})};
        }
        if (row.id === 'remote.listen') return {present: !!status?.listener, displayValue: status?.listener && `${status.listener.address}:${status.listener.port}`};
        if (row.id === 'remote.addresses') {
          return {present: !!status, wide: true, control: <div>{status?.addresses.map(address => <p key={`${address.address}:${address.port}`}>{address.address}:{address.port}</p>)}</div>};
        }
        if (remoteSettings.has(row.id)) {
          const state = source.rowState(row, selected);
          if (row.id === 'remote.interface') {
            const interfaces = remote.interfaces, chosen = interfaces?.selected;
            const choices = [{value: 'all', label: defaultI18n.t('next.remoteOwner.automatic')}, ...(interfaces?.interfaces ?? []).map(item => ({value: item.name, label: item.name, hint: item.addresses.join(', ')}))];
            if (chosen && !choices.some(item => item.value === chosen)) choices.push({value: chosen, label: chosen});
            return {...state, disabled, choices, help: interfaces?.note ?? state.help};
          }
          if (row.id === 'remote.access') {
            return {...state, disabled, choices: row.control.options?.filter(option => option.value !== 'private').map(option => ({...option, label: defaultI18n.t(option.label), hint: option.hint && defaultI18n.t(option.hint)}))};
          }
          if (row.id === 'remote.secureConnections' && status?.remoteSecurity.active === 'enhanced') {
            return {...state, disabled: true, readOnly: true, value: 'required', displayValue: defaultI18n.t('next.remoteOwner.required')};
          }
          return {...state, disabled};
        }
      }
      if (row.id === 'metadata.perLibrary') return {present: current() && controller.sectionVisible(selected.section)};
      return undefined;
    },
    special: (row, state, rowContext, _openPanel, controller) => {
      if (remoteStringListRows.has(row.id)) {
        return <SettingsStringList mono label={defaultI18n.t(row.label)} values={Array.isArray(state.value) ? state.value as string[] : []} addLabel={defaultI18n.t(row.control.addLabel!)} disabled={state.disabled} onChange={value => controller.change(row, value, rowContext)} />;
      }
      if (row.id === 'metadata.perLibrary') {
        return <Button variant="glass" size="small" onClick={() => openSettings('libraries')}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
      }
      return undefined;
    },
    section: (selected, _openPanel, renderRows, controller) => {
      if (selected.section.scope === 'server' && selected.section.id === 'remote') {
        return remoteSettings ? <OwnerRemotePage source={remoteSettings}
          onConfirmed={() => {void controller.ownerSettingsChanged().catch(reportActionError);}}
          groups={renderRows({groups: selected.section.groups})}
          advanced={selected.section.advanced ? renderRows({advanced: selected.section.advanced}) : null} /> : null;
      }
      if (selected.section.scope === 'server' && selected.section.id === 'metadata' && metadataCurrent()) {
        return <MetadataSettingsPage api={api} isCurrent={metadataCurrent} onLibraries={() => openSettings('libraries')} reportError={reportError}
          recommendations={renderRows({groups: selected.section.groups?.filter(group => group.rows.some(row => row.id === 'metadata.titleDataset'))})} />;
      }
      if (selected.section.id === 'remote-sources') {
        return <RemoteSourcesPage api={api} serverId={viewer.serverId} viewerId={viewerId} isCurrent={current} reportError={reportError} openSection={openSettings} />;
      }
      return undefined;
    },
    dispose: () => {remoteSettings?.dispose();},
  };

  return {surface, remoteSettings};
}
