import type {Routes} from '../../../../../../packages/contracts/src/next/api.ts';
import type {ServerTier} from '@core/server-admin/route-access.ts';

/**
 * The parts of Troubleshooting and the routes each one calls. Who sees a part is never listed
 * here: it is read from those routes' published access (next/api.ts, checked below), so a tier is
 * only offered a part whose every call the server allows it (spec and owner-server 43a:
 * administrators share part of this page).
 */
export const troubleshootingRoutes = Object.freeze({
  messages: ['GET /v1/admin/logs', 'GET /v1/admin/logs/events'],
  detailWindow: ['PUT /v1/admin/logs/detail'],
  logSettings: ['GET /v1/admin/settings/{name}', 'PUT /v1/admin/settings/{name}'],
  feedback: ['GET /v1/admin/reports', 'GET /v1/admin/reports/{id}', 'PATCH /v1/admin/reports/{id}'],
  // App logs the apps sent, with the server's records: drawn on the Reports viewer.
  reports: ['GET /v1/admin/reports', 'GET /v1/admin/reports/{id}', 'GET /v1/admin/reports/{id}/log', 'DELETE /v1/admin/reports/{id}', 'GET /v1/admin/logs', 'GET /v1/admin/audit'],
  // Stuck files (titles a rebuild set aside): on the same viewer, and its own part, because the spec
  // gives administrators "Messages and stuck files" while app logs stay the owner's.
  stuckFiles: ['GET /v1/admin/skipped'],
  // What this server can do, its data folder's permissions, and the export made when asked (definition 12 rules 74 to 76).
  support: ['GET /v1/admin/capabilities', 'GET /v1/admin/data-folder/permissions', 'POST /v1/admin/data-folder/permissions/fix', 'POST /v1/admin/support-export'],
} satisfies Record<string, readonly (keyof Routes)[]>);

// The published access of each route a part calls; the literal values are checked against the route table.
const rebuiltAccess={
 'GET /v1/admin/logs':'admin','GET /v1/admin/logs/events':'admin',
 'GET /v1/admin/settings/{name}':'owner','PUT /v1/admin/settings/{name}':'owner',
 'GET /v1/admin/reports':'owner','GET /v1/admin/reports/{id}':'owner','GET /v1/admin/reports/{id}/log':'owner','PATCH /v1/admin/reports/{id}':'owner','DELETE /v1/admin/reports/{id}':'owner',
 'GET /v1/admin/audit':'owner','GET /v1/admin/skipped':'admin',
 'GET /v1/admin/capabilities':'owner','GET /v1/admin/data-folder/permissions':'owner','POST /v1/admin/data-folder/permissions/fix':'owner','POST /v1/admin/support-export':'owner',
} as const satisfies {[K in keyof Routes]?:Routes[K]['access']};
/** Record more detail is the owner's (definition 12: "Owner: read messages, record more detail"; administrators get
 * Messages). It stays the owner's here whatever access the server publishes for it. */
const ownerOnly: ReadonlySet<keyof Routes> = new Set<keyof Routes>(['PUT /v1/admin/logs/detail']);

export type TroubleshootingPart = keyof typeof troubleshootingRoutes;

/** The parts of Troubleshooting this tier may use. */
export function troubleshootingParts(tier: ServerTier): ReadonlySet<TroubleshootingPart> {
  // A route with no access listed here is the owner's.
  const allowed = (id: keyof Routes) => tier === 'owner' || !ownerOnly.has(id) && (rebuiltAccess as Partial<Record<keyof Routes, string>>)[id] === 'admin';
  return new Set((Object.keys(troubleshootingRoutes) as TroubleshootingPart[]).filter(part => troubleshootingRoutes[part].every(allowed)));
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
