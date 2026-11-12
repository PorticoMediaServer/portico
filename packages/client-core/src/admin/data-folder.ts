import {defaultI18n} from '../../../i18n/src/index.ts';
import type {DataFolderPermissions as WireDataFolderPermissions} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

/** Who may open the server's data folder (server-next `GET /v1/admin/data-folder/permissions`, owner only), as the
 * server sends it. While `readableByOthers`, the Dashboard has the alert `data_folder_readable`; `fixable` says whether
 * Fix permissions can be made, and where it cannot, `reason` and `message` say why. Shared by the Dashboard's alert
 * and Settings > Troubleshooting. */
export type DataFolderPermissions = Readonly<WireDataFolderPermissions>;

export class DataFolderPermissionsError extends Error {
  readonly code = 'invalid_data_folder_permissions';
  constructor() { super('invalid_data_folder_permissions'); this.name = 'DataFolderPermissionsError'; }
}
const text = (v: unknown) => typeof v === 'string' && v.length <= 4096;
export function parseDataFolderPermissions(value: unknown): DataFolderPermissions {
  const v = value as Record<string, unknown> | null;
  if (!v || typeof v !== 'object' || !text(v.path) || !text(v.owner) || !text(v.account) || typeof v.readableByOthers !== 'boolean' ||
      typeof v.fixable !== 'boolean' || typeof v.checked !== 'boolean' || v.reason !== undefined && !text(v.reason) || v.message !== undefined && !text(v.message)) throw new DataFolderPermissionsError();
  return Object.freeze({path: v.path as string, owner: v.owner as string, account: v.account as string, readableByOthers: v.readableByOthers, fixable: v.fixable, checked: v.checked,
    ...(v.reason ? {reason: v.reason as string} : {}), ...(v.message ? {message: v.message as string} : {})});
}

export async function readDataFolderPermissions(api: ServerRequester, signal?: AbortSignal): Promise<DataFolderPermissions> {
  return parseDataFolderPermissions(await callServer(api, 'GET /v1/admin/data-folder/permissions', {signal}));
}
/** Makes the data folder private to its owner once the owner has confirmed (`dataFolderConfirmText`). Safe to send again.
 * A refusal (409: not_kept_here, not_folder_owner, read_only_file_system, folder_changed, not_checked_here) rejects with
 * the server's error, whose message is the server's own words. Answers the folder as it is now. */
export async function fixDataFolderPermissions(api: ServerRequester, signal?: AbortSignal): Promise<DataFolderPermissions> {
  return parseDataFolderPermissions(await callServer(api, 'POST /v1/admin/data-folder/permissions/fix', {signal}));
}
/** What Fix permissions will change, asked before it is sent. */
export function dataFolderConfirmText(owner: string, path: string): string {
  return defaultI18n.t('next.dashboard.fixPermissionsConfirm', {owner, path});
}
