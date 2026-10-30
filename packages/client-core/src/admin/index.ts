/**
 * The owner console surface of client-core: administration, the server console,
 * telemetry, inventory, remote sources and media analysis. Viewer apps import
 * the main entry (`@portico/client-core`), which no longer re-exports these, so
 * an app that never shows the console doesn't bundle them (PERF-15).
 */
export * from '../console.ts';
export * from '../server-administration.ts';
export * from '../backups.ts';
export * from '../api-access.ts';
export * from '../telemetry.ts';
export * from '../attention.ts';
export * from '../playback-history.ts';
export * from "../library-inventory.ts";
export * from "../title-identity.ts";
export * from "../administration.ts";
export {RemoteSourceService,readSourceAvailability,sourceAvailabilityMessage,sourceReadPreset,sourceReadPresetName} from "../remote-sources.ts";
export type {RemoteSource,RemoteReceipt,RemoteState,DAVConfiguration,AnalysisPolicy,SourceAvailability,NetworkPolicy,SourceReadValues,SourceReadSettings,SourceReadPreset} from "../remote-sources.ts";
export * from "../media-analysis.ts";
export * from "../segment-review.ts";

export * from '../storage-status.ts';
export * from './activity-summary/index.ts';

export * from './dashboard.ts';

export * from '../report-context.ts';
export {SettingsPeopleClient, parseSettingsPeoplePage} from '../settings-people.ts';
export type {SettingsPersonRow, SettingsPeoplePage, SettingsPeopleQuery, SettingsBulkAccess, SettingsBulkAction} from '../settings-people.ts';
