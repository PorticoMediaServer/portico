import type {Capability, DataFolderPermissions} from '../../../contracts/src/next/api.ts';
import type {HttpLocalApi} from '../index.ts';
import {callServer} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {SUPPORT_COMPONENTS, type SupportComponent} from './support-source.ts';

export const OWNER_SUPPORT_WINDOWS = [60, 360, 1440, 4320, 10080] as const;
export type OwnerSupportOptions = Readonly<{windowMinutes: typeof OWNER_SUPPORT_WINDOWS[number]; components: readonly SupportComponent[]; includePaths: boolean}>;
export type OwnerSupportFacts = Readonly<{capabilities: readonly Capability[]; permissions: DataFolderPermissions}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const invalid = (): never => {throw new Error(unreadableServerResponse);};
const text = (value: unknown) => typeof value === 'string';

/** The actual rebuilt facts, including checking/not_used and an unfixable permissions warning. */
export async function readOwnerSupport(api: Pick<HttpLocalApi, 'request'>, signal?: AbortSignal): Promise<OwnerSupportFacts> {
  const [capabilities, permissions] = await Promise.all([
    callServer(api, 'GET /v1/admin/capabilities', {signal}),
    callServer(api, 'GET /v1/admin/data-folder/permissions', {signal}),
  ]);
  if (!Array.isArray(capabilities.items) || capabilities.items.length > 64 || capabilities.items.some(c => !object(c) ||
    !['ffmpeg', 'graphics', 'folders', 'protection'].includes(c.area) || !text(c.name) ||
    !['works', 'does_not_work', 'checking', 'not_used'].includes(c.state) || [c.reason, c.detail, c.path].some(v => v !== undefined && !text(v)))) invalid();
  if (!permissions || ![permissions.path, permissions.owner, permissions.account].every(text) ||
    ![permissions.readableByOthers, permissions.fixable, permissions.checked].every(v => typeof v === 'boolean') ||
    [permissions.reason, permissions.message].some(v => v !== undefined && !text(v))) invalid();
  return Object.freeze({capabilities: Object.freeze(capabilities.items.map(c => Object.freeze({...c}))), permissions: Object.freeze({...permissions})});
}

/** The page offers this only while the read fact says readableByOthers AND fixable. */
export const fixOwnerSupportPermissions = (api: Pick<HttpLocalApi, 'request'>, signal?: AbortSignal): Promise<DataFolderPermissions> =>
  callServer(api, 'POST /v1/admin/data-folder/permissions/fix', {signal});

/** Ready means a real local ZIP is in hand. There is no server export id, store or expiry to invent. */
export async function prepareOwnerSupportExport(api: Pick<HttpLocalApi, 'downloadSupportExport'>, options: OwnerSupportOptions, signal?: AbortSignal): Promise<Readonly<{blob: Blob; filename: string; options: OwnerSupportOptions}>> {
  if (!OWNER_SUPPORT_WINDOWS.includes(options.windowMinutes) || typeof options.includePaths !== 'boolean' || !options.components.length || options.components.length > 4 ||
    new Set(options.components).size !== options.components.length || options.components.some(c => !SUPPORT_COMPONENTS.includes(c))) invalid();
  const captured = Object.freeze({...options, components: Object.freeze([...options.components])});
  const blob = await api.downloadSupportExport(captured, signal);
  return Object.freeze({blob, filename: 'portico-support.zip', options: captured});
}
