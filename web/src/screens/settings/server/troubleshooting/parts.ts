import type {RouteId} from '../../../../../../packages/contracts/src/route-paths.ts';
import {serverTierMayCallAll, type ServerTier} from '@core/server-admin/route-access.ts';

/**
 * The parts of Troubleshooting and the routes each one calls. Who sees a part is never listed
 * here: it is read from those routes' published access (routes.ts), so a tier is only offered a
 * part whose every call the server allows it (spec and owner-server 43a: administrators share
 * part of this page).
 */
export const troubleshootingRoutes = Object.freeze({
  messages: ['get_admin_logs', 'get_admin_logs_events', 'get_admin_logs_settings'],
  detailWindow: ['post_admin_logs_debug_window'],
  logSettings: ['patch_admin_logs_settings'],
  feedback: ['get_admin_feedback_reports', 'get_admin_feedback_reports_id', 'post_admin_feedback_reports_id_status'],
  reports: ['get_client_logs', 'get_client_log', 'download_client_log', 'delete_client_log', 'get_catalogue_failures', 'get_admin_console_diagnostics_lane', 'post_admin_console_diagnostics_capture'],
  support: ['get_capability_report', 'get_admin_console_alerts', 'get_console_registry', 'post_admin_console_exports', 'get_admin_console_exports_id'],
  problems: ['list_playback_failure_memory', 'clear_playback_failure_memory'],
} satisfies Record<string, readonly RouteId[]>);

export type TroubleshootingPart = keyof typeof troubleshootingRoutes;

/** The parts of Troubleshooting this tier may use. */
export function troubleshootingParts(tier: ServerTier): ReadonlySet<TroubleshootingPart> {
  return new Set((Object.keys(troubleshootingRoutes) as TroubleshootingPart[]).filter(part => serverTierMayCallAll(troubleshootingRoutes[part], tier)));
}

/** The part each Troubleshooting row belongs to, so search never offers a row this tier's page does not draw. */
const rowParts: Readonly<Record<string, TroubleshootingPart>> = Object.freeze({
  'troubleshooting.detailWindow': 'detailWindow', 'troubleshooting.messages': 'messages', 'troubleshooting.feedback': 'feedback',
  'troubleshooting.reports': 'reports', 'troubleshooting.support': 'support', 'troubleshooting.prepareExport': 'support', 'troubleshooting.exportPaths': 'support',
  'troubleshooting.exportWindow': 'support', 'troubleshooting.exportRecords': 'support', 'troubleshooting.exportClient': 'support', 'troubleshooting.exportAudit': 'support', 'troubleshooting.exportHealth': 'support',
  'troubleshooting.playbackProblems': 'problems',
  'troubleshooting.logLevel': 'logSettings', 'troubleshooting.detailLength': 'logSettings', 'troubleshooting.keepLogs': 'logSettings', 'troubleshooting.keepLogsByKind': 'logSettings',
});

/** Whether this tier's Troubleshooting page draws the row. A row outside Troubleshooting is not this
 *  page's to decide, and the owner keeps every row; a row with no known part is the owner's only. */
export function troubleshootingRowShown(rowId: string, tier: ServerTier): boolean {
  if (tier === 'owner' || !rowId.startsWith('troubleshooting.')) return true;
  const part = rowParts[rowId];
  return !!part && troubleshootingParts(tier).has(part);
}
