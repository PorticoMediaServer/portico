import type {RouteId} from '../../../../../../packages/contracts/src/route-paths.ts';
import type {Routes} from '../../../../../../packages/contracts/src/next/api.ts';
import {serverTierMayCall, type ServerTier} from '@core/server-admin/route-access.ts';

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
  // App logs the apps sent, with the server's records: drawn on the Reports viewer.
  reports: ['GET /v1/admin/reports', 'GET /v1/admin/reports/{id}', 'GET /v1/admin/reports/{id}/log', 'DELETE /v1/admin/reports/{id}', 'GET /v1/admin/logs', 'GET /v1/admin/audit'],
  // Stuck files (titles a rebuild set aside): on the same viewer, and its own part, because the spec
  // gives administrators "Messages and stuck files" while app logs stay the owner's.
  stuckFiles: ['GET /v1/admin/skipped'],
  support: ['get_capability_report', 'get_admin_console_alerts', 'get_console_registry', 'post_admin_console_exports', 'get_admin_console_exports_id'],
} satisfies Record<string, readonly (RouteId | keyof Routes)[]>);

// These parts use the rebuilt route table; the literal access values are checked against it.
const rebuiltAccess={
 'GET /v1/admin/reports':'owner','GET /v1/admin/reports/{id}':'owner','GET /v1/admin/reports/{id}/log':'owner','DELETE /v1/admin/reports/{id}':'owner',
 'GET /v1/admin/logs':'admin','GET /v1/admin/audit':'owner','GET /v1/admin/skipped':'admin',
} as const satisfies {[K in keyof Routes]?:Routes[K]['access']};

export type TroubleshootingPart = keyof typeof troubleshootingRoutes;

/** The parts of Troubleshooting this tier may use. */
export function troubleshootingParts(tier: ServerTier): ReadonlySet<TroubleshootingPart> {
  return new Set((Object.keys(troubleshootingRoutes) as TroubleshootingPart[]).filter(part => troubleshootingRoutes[part].every(id=>{const access=rebuiltAccess[id as keyof typeof rebuiltAccess];return access?tier==='owner'||access==='admin':serverTierMayCall(id as RouteId,tier);})));
}

/** The part each Troubleshooting row belongs to, so search never offers a row this tier's page does not draw. */
const rowParts: Readonly<Record<string, TroubleshootingPart | readonly TroubleshootingPart[]>> = Object.freeze({
  'troubleshooting.detailWindow': 'detailWindow', 'troubleshooting.messages': 'messages', 'troubleshooting.feedback': 'feedback',
  'troubleshooting.reports': ['reports', 'stuckFiles'] as const, 'troubleshooting.support': 'support', 'troubleshooting.prepareExport': 'support', 'troubleshooting.exportPaths': 'support',
  'troubleshooting.exportWindow': 'support', 'troubleshooting.exportRecords': 'support', 'troubleshooting.exportClient': 'support', 'troubleshooting.exportAudit': 'support', 'troubleshooting.exportHealth': 'support',
  'troubleshooting.logLevel': 'logSettings', 'troubleshooting.detailLength': 'logSettings', 'troubleshooting.keepLogs': 'logSettings', 'troubleshooting.keepLogsByKind': 'logSettings',
});

/** Whether this tier's Troubleshooting page draws the row. A row outside Troubleshooting is not this
 *  page's to decide, and the owner keeps every row; a row with no known part is the owner's only. */
export function troubleshootingRowShown(rowId: string, tier: ServerTier): boolean {
  if (tier === 'owner' || !rowId.startsWith('troubleshooting.')) return true;
  const part = rowParts[rowId], parts = troubleshootingParts(tier);
  return !!part && (typeof part === 'string' ? [part] : part).some(name => parts.has(name));
}
