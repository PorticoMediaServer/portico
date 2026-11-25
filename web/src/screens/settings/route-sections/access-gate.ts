import type {Routes} from '../../../../../packages/contracts/src/next/api.ts';
import type {ServerTier} from '@core/server-admin/route-access.ts';
import {troubleshootingParts} from '../server/troubleshooting/parts';
import type {SettingsRouteRole} from './types';

/**
 * Contract-checked role and capability gate for server Settings sections, modeled on
 * `server/troubleshooting/parts.ts`. Each section names the real server routes its adapters call,
 * and every route's access tier is statically checked against `packages/contracts/src/next/api.ts`.
 */
export const serverSectionRoutes = Object.freeze({
  general: ['GET /v1/admin/updates', 'POST /v1/admin/restart', 'GET /v1/admin/background/pause'],
  libraries: ['GET /v1/admin/libraries', 'POST /v1/admin/libraries/{id}/scan'],
  streaming: ['GET /v1/admin/hardware', 'POST /v1/admin/hardware/test'],
  storage: ['GET /v1/admin/storage', 'GET /v1/admin/backups'],
  remote: ['GET /v1/admin/settings/remote-access', 'GET /v1/admin/remote-access'],
  metadata: ['GET /v1/admin/metadata/settings', 'PUT /v1/admin/metadata/settings'],
  integrations: ['GET /v1/admin/api-keys', 'GET /v1/admin/webhooks'],
  live: ['GET /v1/admin/live/sources', 'GET /v1/admin/library-channels'],
  segments: ['GET /v1/admin/segments', 'POST /v1/admin/segments/redetect'],
  'remote-sources': ['GET /v1/admin/remote-sources'],
  people: ['GET /v1/admin/members', 'GET /v1/admin/invitations'],
  signin: ['GET /v1/admin/sign-in'],
  dashboard: ['GET /v1/admin/sessions'],
  history: ['GET /v1/admin/play-history'],
  troubleshooting: ['GET /v1/admin/logs', 'GET /v1/admin/skipped'],
} satisfies Record<string, readonly (keyof Routes)[]>);

// Published access of every route above; TypeScript checks each literal against `Routes[K]['access']`.
const sectionRouteAccess = {
  'GET /v1/admin/updates': 'owner',
  'POST /v1/admin/restart': 'owner',
  'GET /v1/admin/background/pause': 'owner',
  'GET /v1/admin/libraries': 'owner',
  'POST /v1/admin/libraries/{id}/scan': 'owner',
  'GET /v1/admin/hardware': 'owner',
  'POST /v1/admin/hardware/test': 'owner',
  'GET /v1/admin/storage': 'owner',
  'GET /v1/admin/backups': 'owner',
  'GET /v1/admin/settings/remote-access': 'owner',
  'GET /v1/admin/remote-access': 'owner',
  'GET /v1/admin/metadata/settings': 'owner',
  'PUT /v1/admin/metadata/settings': 'owner',
  'GET /v1/admin/api-keys': 'owner',
  'GET /v1/admin/webhooks': 'owner',
  'GET /v1/admin/live/sources': 'owner',
  'GET /v1/admin/library-channels': 'owner',
  'GET /v1/admin/segments': 'owner',
  'POST /v1/admin/segments/redetect': 'owner',
  'GET /v1/admin/remote-sources': 'owner',
  'GET /v1/admin/members': 'admin',
  'GET /v1/admin/invitations': 'admin',
  'GET /v1/admin/sign-in': 'owner',
  'GET /v1/admin/sessions': 'admin',
  'GET /v1/admin/play-history': 'admin',
  'GET /v1/admin/logs': 'admin',
  'GET /v1/admin/skipped': 'admin',
} as const satisfies {[K in keyof Routes]?: Routes[K]['access']};

export type ServerSettingsSectionId = keyof typeof serverSectionRoutes;

export type ServerSectionGateInput = Readonly<{
  role: SettingsRouteRole;
  primaryProfile: boolean;
  capabilities?: Readonly<{manageServer?: boolean; managePeople?: boolean; manageSignIn?: boolean}>;
  signIn?: string;
  authority?: string;
}>;

/** Whether `role` is allowed to call every route published for `section`. */
export function serverSectionTierAllowed(section: ServerSettingsSectionId, role: SettingsRouteRole): boolean {
  if (role === 'member') return false;
  if (section === 'troubleshooting') return troubleshootingParts(role as ServerTier).size > 0;
  if (role === 'owner') return true;
  return serverSectionRoutes[section].every(route => (sectionRouteAccess as Partial<Record<keyof Routes, string>>)[route] === 'admin');
}

/** Shared role + primary-profile + capability + sign-in gate for every server Settings section adapter. */
export function canManageServerSection(section: ServerSettingsSectionId, input: ServerSectionGateInput): boolean {
  if (!input.primaryProfile || !serverSectionTierAllowed(section, input.role)) return false;
  const capabilities = input.capabilities;
  if (section === 'people') {
    return capabilities?.managePeople === true && (input.authority === undefined || input.authority === 'local' || input.authority === 'hosted');
  }
  if (section === 'signin') {
    return capabilities?.manageSignIn === true && (input.signIn === 'local' || input.signIn === 'portico');
  }
  if (capabilities?.manageServer !== true) return false;
  if (section === 'remote-sources') return input.signIn === 'local';
  return true;
}

/** Convenience check matching a route section id against the shared gate. */
export function activeServerSection(
  route: Readonly<{scope: string; section: string}>,
  section: ServerSettingsSectionId,
  input: ServerSectionGateInput,
): boolean {
  return route.scope === 'server' && route.section === section && canManageServerSection(section, input);
}
