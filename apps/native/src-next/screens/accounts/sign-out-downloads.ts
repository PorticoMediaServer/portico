import type {AppServerSession} from '../../../../../packages/client-core/src/app-sessions';
import {sameServerPin} from '../../../../../packages/client-core/src/route-identity';
import type {OfflineEntry} from '../../../platform/offline-downloads';
import {offlineOwnerKey} from '../../../platform/offline-downloads';

/** The exact viewer whose sign-out sheet is open. Sign-out itself always uses this
 * captured family; downloads are touched only while this same viewer is still selected. */
export type SignOutCapturedSession = AppServerSession;

export function capturedViewerIsCurrent(selected: AppServerSession | undefined, captured: SignOutCapturedSession): boolean {
  if (!selected) return false;
  const connection = selected.connection, expected = captured.connection;
  return sameServerPin(connection.pin, expected.pin)
    && connection.session.sessionFamilyId === expected.session.sessionFamilyId
    && (['serverId', 'accountId', 'profileId', 'authority'] as const).every(key => connection.session.viewer[key] === expected.session.viewer[key]);
}

const entryOwner = (entry: OfflineEntry) => entry.receipt ? offlineOwnerKey(entry.receipt.claims.viewer) : entry.owner;

export type CapturedDownloadPorts = Readonly<{
  /** The actual native selection now, never the rendered servers snapshot. */
  selected: () => Promise<AppServerSession | undefined>;
  entries: () => readonly OfflineEntry[];
  /** The caller must re-resolve the live entry and its owner before deleting it. */
  remove: (entry: OfflineEntry) => Promise<void>;
}>;

/** Deletes only the captured viewer's downloads. The selection is reread before every
 * removal, so a sheet left open across a profile switch deletes nothing from the new
 * viewer and stops as soon as the captured viewer retires. */
export async function removeCapturedDownloads(captured: SignOutCapturedSession, ports: CapturedDownloadPorts): Promise<number> {
  const owner = offlineOwnerKey(captured.connection.session.viewer), owned = (entry: OfflineEntry) => entryOwner(entry) === owner;
  let removed = 0;
  for (const candidate of ports.entries().filter(owned)) {
    if (!capturedViewerIsCurrent(await ports.selected(), captured)) break;
    const live = ports.entries().find(entry => entry.id === candidate.id && owned(entry));
    if (!live) continue;
    await ports.remove(live);
    removed++;
  }
  return removed;
}

/** The sheet's captured server shape; the downloads helper always receives its session. */
export type CapturedServer = Readonly<{session: SignOutCapturedSession; name: string}>;

export function removeCapturedServerDownloads(captured: CapturedServer, ports: CapturedDownloadPorts): Promise<number> {
  return removeCapturedDownloads(captured.session, ports);
}
