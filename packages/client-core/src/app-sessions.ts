import type {ServerConnectionStorage, RememberedServer} from './server-connections.ts';
import {parseRememberedServer} from './remembered-server.ts';
import {RouteError, routeOrigin, type KnownServerRoute} from './route-identity.ts';

export type SignInKind = 'portico' | 'local';
export type PorticoAccountScope = Readonly<{origin: string; accountId: string}>;
export type AppServerSession = Readonly<{signIn: SignInKind; connection: RememberedServer; resetFileOwner?: true}>;
export type AppSessionRecord = Readonly<{
  version: 1; revision: number; selectedServerId?: string; servers: readonly AppServerSession[];
}>;
/** Protected credential storage. `change` MUST atomically read, call update, and replace the
 * record; a rejected callback/write leaves the old record intact. If supplied, beforeCommit
 * must run inside the storage lock immediately before scheduling any durable mutation,
 * including after asynchronous encryption. Absent is undefined. */
export interface AppSessionStorage {
  read(): Promise<AppSessionRecord | undefined>;
  change(update: (current: AppSessionRecord | undefined) => AppSessionRecord | undefined, beforeCommit?: () => void): Promise<void>;
}
export type NewServerConnection = Readonly<{
  signIn: SignInKind; hostedAccount?: PorticoAccountScope; resetFileOwner?: true;
  /** Captured before sign-in begins. A later selection/sign-out invalidates this admission. */
  expectedRevision: number;
}>;
const empty = (): AppSessionRecord => ({version: 1, revision: 0, servers: []});
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const id = (v: unknown): v is string => typeof v === 'string' && v.length > 0 && v.length <= 256 && !/[\p{Cc}\p{Cf}]/u.test(v);
function invalid(): never { throw new RouteError('invalid_saved_connection', 'Saved app sign-ins are incomplete.'); }
function cancelled(): never { throw new RouteError('cancelled', 'This server sign-in changed or ended on this device.'); }
function scope(raw: PorticoAccountScope): PorticoAccountScope {
  if (!id(raw.accountId)) invalid();
  const origin = routeOrigin(raw.origin), url = new URL(origin);
  if (origin !== raw.origin || url.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)) invalid();
  return {origin, accountId: raw.accountId};
}
/** The reset-file owner grant is server-local, access-only and tied to its
 * proved issuer. It supplies no Hosted account or directory route authority. */
function resetOwner(connection: RememberedServer): boolean {
  const session = connection.session;
  return !connection.hosted && session.viewer.authority === 'local' && session.viewer.role === 'owner' && !session.refreshToken
    && session.serverIdentity?.publicKey === connection.pin.publicKey && session.serverIdentity.fingerprint === connection.pin.fingerprint;
}
export function parseAppSessionRecord(raw: unknown): AppSessionRecord {
  if (raw === undefined || raw === null) return empty();
  if (!object(raw) || raw.version !== 1 || !Number.isSafeInteger(raw.revision) || Number(raw.revision) < 0 || !Array.isArray(raw.servers) || raw.servers.length > 1000) invalid();
  const servers: AppServerSession[] = raw.servers.map(value => {
    if (!object(value) || !['portico', 'local'].includes(String(value.signIn))) invalid();
    const connection = parseRememberedServer(value.connection);
    if (!connection || connection.session.viewer.authority !== 'local') invalid();
    if (value.resetFileOwner !== undefined && value.resetFileOwner !== true) invalid();
    if (value.resetFileOwner === true) {if (value.signIn !== 'portico' || !resetOwner(connection)) invalid();}
    else if (value.signIn === 'portico' ? !connection.hosted : !!connection.hosted) invalid();
    if (connection.hosted) scope(connection.hosted);
    return {signIn: value.signIn as SignInKind, connection, ...(value.resetFileOwner === true ? {resetFileOwner: true as const} : {})};
  });
  const ids = servers.map(v => v.connection.pin.serverId);
  if (new Set(ids).size !== ids.length || raw.selectedServerId !== undefined && (!id(raw.selectedServerId) || !ids.includes(raw.selectedServerId))) invalid();
  // One Hosted account per app. Its id is not the server-local membership id.
  const bindings = new Set(servers.flatMap(v => v.connection.hosted ? [JSON.stringify([v.connection.hosted.origin, v.connection.hosted.accountId])] : []));
  if (bindings.size > 1) invalid();
  return {version: 1, revision: raw.revision as number, ...(raw.selectedServerId === undefined ? {} : {selectedServerId: raw.selectedServerId as string}), servers};
}
/** Public-only snapshot for first contact and native certificate decisions. Local addresses
 * come from the exact protected record, never from a link, GET /system or an unverified route
 * document. Portico's signed candidates are resolved by connectHostedServer separately. */
export function knownServerRoutes(record:AppSessionRecord):readonly KnownServerRoute[] {
 return Object.freeze(parseAppSessionRecord(record).servers.map(({signIn,connection})=>Object.freeze({
  signIn,pin:Object.freeze({...connection.pin}),origins:Object.freeze([...new Set([
   connection.logicalOrigin,...connection.routes.paired,...(connection.routes.lastGood?[connection.routes.lastGood]:[]),
  ])]),
 })));
}
function next(old: AppSessionRecord, servers: readonly AppServerSession[], selectedServerId = old.selectedServerId): AppSessionRecord {
  if (old.revision >= Number.MAX_SAFE_INTEGER) invalid();
  return parseAppSessionRecord({version: 1, revision: old.revision + 1, servers,
    ...(selectedServerId && servers.some(v => v.connection.pin.serverId === selectedServerId) ? {selectedServerId} : {})});
}
/** Several independent server families, with a selected server as navigation state only.
 * Scoped adapters keep refresh and route persistence on the original server after switching. */
export class AppSessionStore {
  private readonly storage: AppSessionStorage;
  constructor(storage: AppSessionStorage) { this.storage = storage; }
  async read(): Promise<AppSessionRecord> { return parseAppSessionRecord(await this.storage.read()); }
  async selected(): Promise<AppServerSession | undefined> {
    const value = await this.read(); return value.servers.find(v => v.connection.pin.serverId === value.selectedServerId);
  }
  async select(serverId: string, isCurrent: () => boolean = () => true, expectedRevision?: number): Promise<void> {
    if (!id(serverId) || expectedRevision !== undefined && (!Number.isSafeInteger(expectedRevision) || expectedRevision < 0)) invalid();
    const beforeCommit = () => { if (!isCurrent()) cancelled(); };
    await this.storage.change(raw => {
      beforeCommit();
      const old = parseAppSessionRecord(raw);
      if (expectedRevision !== undefined && old.revision !== expectedRevision) cancelled();
      if (!old.servers.some(v => v.connection.pin.serverId === serverId)) cancelled();
      return next(old, old.servers, serverId);
    }, beforeCommit);
  }
  connectionStorage(serverId: string, admission?: NewServerConnection): ServerConnectionStorage {
    if (!id(serverId)) invalid();
    if (admission && (!Number.isSafeInteger(admission.expectedRevision) || admission.expectedRevision < 0 || !['portico', 'local'].includes(admission.signIn) || admission.resetFileOwner !== undefined && admission.resetFileOwner !== true || (admission.resetFileOwner ? admission.signIn !== 'portico' || !!admission.hostedAccount : (admission.signIn === 'portico') !== !!admission.hostedAccount))) invalid();
    const account = admission?.hostedAccount ? scope(admission.hostedAccount) : undefined;
    let admitted = false;
    let previous: AppServerSession | undefined, accepted: RememberedServer | undefined;
    return {
      read: async () => (await this.read()).servers.find(v => v.connection.pin.serverId === serverId)?.connection,
      change: async (update, beforeCommit) => {
        await this.storage.change(raw => {
          const old = parseAppSessionRecord(raw), entry = old.servers.find(v => v.connection.pin.serverId === serverId);
          const connection = update(entry?.connection);
          if (connection === entry?.connection) return raw;
          if (connection === undefined) return next(old, old.servers.filter(v => v !== entry));
          const checked = parseRememberedServer(connection);
          if (!checked || checked.pin.serverId !== serverId) invalid();
          const replacement = !entry || checked.selectionId !== entry.connection.selectionId || checked.session.sessionFamilyId !== entry.connection.session.sessionFamilyId;
          const rollback = admitted && !!previous && connection === previous.connection && entry?.connection.selectionId === accepted?.selectionId && entry?.connection.session.sessionFamilyId === accepted?.session.sessionFamilyId;
          if (replacement && !rollback && (!admission || admitted || old.revision !== admission.expectedRevision)) cancelled();
          const signIn = rollback ? previous!.signIn : replacement ? admission!.signIn : entry!.signIn;
          const resetFileOwner = rollback ? previous!.resetFileOwner : replacement ? admission!.resetFileOwner : entry!.resetFileOwner;
          if (resetFileOwner ? signIn !== 'portico' || !resetOwner(checked) : signIn === 'portico' && (!checked.hosted || account && (checked.hosted.origin !== account.origin || checked.hosted.accountId !== account.accountId)) || signIn === 'local' && checked.hosted) invalid();
          if (replacement && !rollback) { previous = entry; accepted = checked; }
          const result = next(old, [...old.servers.filter(v => v !== entry), {signIn, connection: checked, ...(resetFileOwner ? {resetFileOwner: true as const} : {})}], replacement ? serverId : old.selectedServerId);
          return result;
        }, beforeCommit);
        // An admission is single use. Rotation, route updates and cleanup need no admission.
        admitted = true;
      },
    };
  }
  /** Captures the selected server on its first read/change. Reuse this adapter for one runtime;
   * construct a fresh one after an explicit server switch. Never follows a later selection. */
  selectedStorage(): ServerConnectionStorage {
    let captured: Promise<ServerConnectionStorage | undefined> | undefined;
    const resolve = () => captured ??= this.selected().then(value => value && this.connectionStorage(value.connection.pin.serverId));
    return {
      read: async () => (await resolve())?.read(),
      change: async (update, beforeCommit) => {
        const selected = await resolve();
        if (!selected) { if (update(undefined) !== undefined) cancelled(); return; }
        await selected.change(update, beforeCommit);
      },
    };
  }
  /** A captured failure may additionally bind the exact record revision and
   * mounted lifetime, including inside the encrypted storage commit lock. */
  async signOutServer(serverId: string, familyId: string, isCurrent: () => boolean = () => true, expectedRevision?: number): Promise<readonly AppServerSession[]> {
    if (!id(serverId) || !id(familyId)) invalid();
    if (expectedRevision !== undefined && (!Number.isSafeInteger(expectedRevision) || expectedRevision < 0)) invalid();
    return this.remove(value => value.connection.pin.serverId === serverId && value.connection.session.sessionFamilyId === familyId, isCurrent, expectedRevision);
  }
  async signOutPorticoAccount(account: PorticoAccountScope): Promise<readonly AppServerSession[]> {
    const checked = scope(account);
    return this.remove(value => value.signIn === 'portico' && value.connection.hosted?.origin === checked.origin && value.connection.hosted.accountId === checked.accountId);
  }
  private async remove(matches: (value: AppServerSession) => boolean, isCurrent: () => boolean = () => true, expectedRevision?: number): Promise<readonly AppServerSession[]> {
    let removed: readonly AppServerSession[] = [];
    const beforeCommit = () => { if (!isCurrent()) cancelled(); };
    await this.storage.change(raw => {
      beforeCommit();
      const old = parseAppSessionRecord(raw);
      if (expectedRevision !== undefined && old.revision !== expectedRevision) cancelled();
      removed = old.servers.filter(matches);
      // Advance the fence even when already absent: a sign-in started before sign-out stays dead.
      return next(old, old.servers.filter(value => !matches(value)));
    }, beforeCommit);
    return removed;
  }
}
