import type {MutableRefObject} from 'react';
import {defaultI18n} from '@i18n';
import {captureReportContext, type ReportContext} from '@core/report-context.ts';
import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import {DashboardScreen, HistoryScreen, createDashboardBindings, type DashboardBindings} from '../../dashboard';
import {createScreenPlayEffect} from '../../browse/playback-effect';
import {LibraryChannelsPage} from '../server/channels/LibraryChannelsPage';
import {libraryChannelAdmission} from '../../live/owner/library-channel-admission';
import {createOwnerSources, OwnerSourcesPage} from '../../live/owner/OwnerSourcesPage';
import {ServerIntros} from '../server-intros/ServerIntros';
import {SegmentIntrosSource} from '../server-intros/source';
import {SegmentsBulkAction} from '../server-intros/BulkAction';
import {IntegrationsPage} from '../server/integrations/ApiKeys';
import {SettingsApiKeysSource} from '../server/integrations/api-keys-source';
import {SettingsWebhooksSource} from '../server/integrations/webhooks-source';
import {createTroubleshootingSurfaces} from '../server/troubleshooting';
import {MessageOptions} from '../server/troubleshooting/MessageOptions';
import type {SettingsController} from '../controller';
import {activeServerSection, type ServerSectionGateInput} from './access-gate';
import {selfLoadedPageSnapshot} from './server-remote-metadata';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

// A carry contains calendar bounds only. The captured session object owns its lifetime.
const historyContexts = new WeakMap<ShellAssemblyContext, ReportContext>();
const segmentsBulkRows = new Set(['segments.redetectAll', 'segments.retryFailed']);
const integrationBuiltInRows = new Set(['integrations.apiKeys', 'integrations.webhooks', 'integrations.assistantAddress', 'integrations.assistantKey']);
const troubleshootingLogRows = new Set(['troubleshooting.logLevel', 'troubleshooting.detailLength', 'troubleshooting.keepLogs', 'troubleshooting.keepLogsByKind']);

const sameCarryInputs = (a: readonly unknown[], b: readonly unknown[]) =>
  a.length === b.length && a.every((value, index) => Object.is(value, b[index]));

export type ReportCarrySlot = MutableRefObject<Readonly<{inputs: readonly unknown[]; report: ReportContext}> | undefined>;

export type ServerMediaOpsHandle = Readonly<{
  surface: SettingsSectionSurface;
  dashboard?: DashboardBindings;
}>;

/** Server › Dashboard & History, Intros & credits, Live TV, Integrations (API keys & webhooks), and Troubleshooting. */
export function createServerMediaOpsSection(
  env: SettingsRouteEnv,
  controller: SettingsController,
  gate: ServerSectionGateInput,
  reportCarry: ReportCarrySlot,
  hasSnapshot: boolean,
): ServerMediaOpsHandle {
  const {api, originalApi, context, buildContext, route, role, viewer, viewerId, current, assert, signal, reportError, reportActionError, openHome, openSettings, mediaRuntime, reportTimeZone, facts} = env;

  let dashboard: DashboardBindings | undefined;
  if (activeServerSection(route, 'dashboard', gate) || activeServerSection(route, 'history', gate)) {
    const pending = route.section === 'history' ? historyContexts.get(context) : undefined;
    if (pending) historyContexts.delete(context);
    const carryInputs = [context, route.scope, route.section, hasSnapshot, reportTimeZone, ...facts];
    const held = reportCarry.current;
    const carried = pending?.timeZone === reportTimeZone ? pending : held && sameCarryInputs(held.inputs, carryInputs) ? held.report : undefined;
    const report = carried ?? captureReportContext(reportTimeZone);
    reportCarry.current = {inputs: carryInputs, report};
    dashboard = createDashboardBindings({
      api, queries: context.freshness?.queries, events: context.events, scope: {serverId: viewer.serverId, viewerId},
      context: report, role: role as 'owner' | 'admin', isCurrent: current,
      isMainProfile: () => current() && context.facts.identity.primaryProfile === true,
      artwork: context.artwork.foreground,
      effects: {openHome, openSettings, reportError, openHistory: next => {if (current()) {historyContexts.set(context, next); openSettings('history');}}},
    });
  }

  // Troubleshooting is shared with administrators: each tier gets the parts whose calls it may make (troubleshooting/parts.ts).
  const troubleshooting = activeServerSection(route, 'troubleshooting', gate)
    ? createTroubleshootingSurfaces({
      tier: role as 'owner' | 'admin', transport: api, serverId: viewer.serverId, viewerId,
      downloadBase: api.baseUrl, isCurrent: current, subscribe: controller.subscribe, reportError,
      messageConnection: {open: async (headers, input) => {assert(); return originalApi.openOwnerLogEvents(headers, signal(input));}},
    }) : undefined;

  const previewLife = new AbortController();
  const preview = createScreenPlayEffect({isCurrent: current, runtime: () => mediaRuntime});
  const intros = activeServerSection(route, 'segments', gate)
    ? new SegmentIntrosSource({api, isCurrent: current, reportError}) : undefined;

  const live = activeServerSection(route, 'live', gate)
    ? createOwnerSources({
      api, serverId: viewer.serverId, viewerId,
      isCurrent: () => current() && context.session().viewer.role === 'owner'
        && context.session().viewer.serverId === viewer.serverId && context.session().viewer.authority === viewer.authority
        && context.session().viewer.accountId === viewer.accountId && context.session().viewer.profileId === viewer.profileId
        && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true,
      reportError,
    }) : undefined;
  const channelPorts = live ? libraryChannelAdmission(buildContext, live) : undefined;

  const integrationKeys = activeServerSection(route, 'integrations', gate)
    ? new SettingsApiKeysSource(api, current, reportError) : undefined;
  const webhooks = integrationKeys ? new SettingsWebhooksSource(api, current, reportError) : undefined;

  const surface: SettingsSectionSurface = {
    getSnapshot: section => section.scope === 'server' && section.id === 'live' && !!live ? selfLoadedPageSnapshot : undefined,
    load: (section, _source, ctrl) => section.scope === 'server' && section.id === 'live' && !!live ? ctrl.loadServerHeader(section) : undefined,
    eligible: (row, selected, ctrl) => {
      if (segmentsBulkRows.has(row.id)) return !!intros && role === 'owner' && current() && ctrl.sectionVisible(selected.section);
      return undefined;
    },
    rowState: (row, selected, ctrl) => {
      if (segmentsBulkRows.has(row.id)) return {present: !!intros && role === 'owner' && current() && ctrl.sectionVisible(selected.section)};
      return undefined;
    },
    special: (row, state) => {
      if (segmentsBulkRows.has(row.id) && intros) {
        return <SegmentsBulkAction row={row} api={api} isCurrent={current} disabled={state.disabled} onDone={() => {void intros.load();}} />;
      }
      return undefined;
    },
    section: (selected, _openPanel, renderRows, ctrl) => {
      if (selected.section.id === 'integrations' && integrationKeys && webhooks) {
        return <IntegrationsPage api={api} source={integrationKeys} webhooks={webhooks} baseUrl={api.baseUrl} isCurrent={current} reportError={reportError}
          otherRows={renderRows({groups: selected.section.groups?.filter(group => !group.rows.some(row => integrationBuiltInRows.has(row.id)))})}
          advancedRows={renderRows({advanced: selected.section.advanced})} />;
      }
      if (selected.section.id === 'live' && live) {
        return <OwnerSourcesPage source={live} tab={selected.pageTab} libraryChannels={<LibraryChannelsPage ports={channelPorts!} />} />;
      }
      if (selected.section.id === 'segments' && intros) {
        return <ServerIntros source={intros} artwork={context.artwork.foreground}
          onPreview={(libraryId, itemId, startSeconds) => {if (current()) void preview({libraryId, itemId, startSeconds}, previewLife.signal, current).catch(reportActionError);}}
          modeRows={renderRows({groups: selected.section.groups?.map(group => ({...group, rows: group.rows.filter(row => row.id === 'segments.mode')})).filter(group => group.rows.length)})}
          advancedRows={renderRows({advanced: selected.section.advanced})} />;
      }
      if (selected.section.id === 'dashboard' && dashboard) return <DashboardScreen bindings={dashboard} embedded />;
      if (selected.section.id === 'history' && dashboard) return <HistoryScreen bindings={dashboard} embedded />;
      if (selected.section.id === 'troubleshooting') {
        return troubleshooting?.render(
          troubleshooting.parts.has('logSettings') && <MessageOptions>{renderRows({
            groups: selected.section.advanced?.groups?.map(group => ({...group, rows: group.rows.filter(row => troubleshootingLogRows.has(row.id))})).filter(group => group.rows.length),
          })}</MessageOptions>,
          selected.section.advanced?.groups?.flatMap(group => group.rows).filter(row => ctrl.rowState(row).changed).map(row => defaultI18n.t(row.label)),
        );
      }
      return undefined;
    },
    dispose: () => {
      previewLife.abort();
      dashboard?.dispose();
      intros?.dispose();
      troubleshooting?.dispose();
      live?.dispose();
      integrationKeys?.dispose();
      webhooks?.dispose();
    },
  };

  return {surface, dashboard};
}
