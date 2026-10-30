import type {AppServerSession, AppSessionStore} from '../../../../packages/client-core/src/app-sessions';

export type NativeServerBinding = Readonly<{serverId: string; familyId: string; fingerprint: string}>;
type ForgetPorts = Readonly<{
  sessions: Pick<AppSessionStore, 'read' | 'signOutServer'>;
  current: () => boolean;
  forgetCertificates: (serverId: string, origins: readonly string[]) => Promise<void>;
  retireRoutes: (records: readonly AppServerSession[]) => void;
}>;
const cancelled = () => Object.assign(new Error('native_forget_retired'), {code: 'cancelled'});
const matches = (record: AppServerSession, binding: NativeServerBinding) =>
  record.connection.pin.serverId === binding.serverId && record.connection.pin.fingerprint === binding.fingerprint &&
  record.connection.session.sessionFamilyId === binding.familyId;

/** Offline only. Certificate acknowledgement precedes removing the sign-in, so a
 * failed native write leaves an entry the person can retry. The final atomic
 * session removal still captures the exact family; a replacement never follows it.
 * There is deliberately no API, fetcher, credentials service or logout port here. */
export async function forgetStoredNativeServer(binding: NativeServerBinding, ports: ForgetPorts): Promise<void> {
  const current = () => {if (!ports.current()) throw cancelled();};
  current();
  const state = await ports.sessions.read(); current();
  const captured = state.servers.find(value => matches(value, binding));
  if (!captured) throw cancelled();
  const record = captured.connection;
  // Every identity-bound certificate is removed by serverId, including origins
  // present only in signed route memory. These additionally cover named origins.
  const origins = [...new Set([record.logicalOrigin, ...record.routes.paired, ...(record.routes.lastGood ? [record.routes.lastGood] : [])])];
  await ports.forgetCertificates(binding.serverId, origins); current();
  const latest = await ports.sessions.read(); current();
  if (!latest.servers.some(value => matches(value, binding))) throw cancelled();
  const removed = await ports.sessions.signOutServer(binding.serverId, binding.familyId);
  // Retire both token bindings if refresh rotated while the native write awaited.
  // Other server entries and their route objects are never touched.
  if (!removed.length) throw cancelled();
  ports.retireRoutes([captured, ...removed]);
}
