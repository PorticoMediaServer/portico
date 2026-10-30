/** Operator driver adapters only. No product API or persistence semantics are replaced. */
import {open, lstat, rename, unlink, realpath} from 'node:fs/promises';
import {constants} from 'node:fs';
import {join, resolve} from 'node:path';
import {pathToFileURL, fileURLToPath} from 'node:url';
import {randomUUID} from 'node:crypto';
import {spawn} from 'node:child_process';
import {HttpLocalApi, type LocalSession} from '../../packages/client-core/src/index.ts';
import {emptyUserMutationState, validateUserMutationState, parseUserMutationReceipt,
  USER_MUTATION_MAX_BYTES, type DurableUserMutationStore, type UserMutationState} from '../../packages/client-core/src/user-mutations.ts';
import {PERSONAL_BATCH_PATH, personalBatchBody, parsePersonalBatchReceipt, type PersonalBatchItem} from '../../packages/client-core/src/personal-saved.ts';
import {SessionsClient} from '../../packages/client-core/src/playback-v1/sessions.ts';
import type {V1Http} from '../../packages/client-core/src/playback-v1/http.ts';
import {routePath} from '../../packages/contracts/src/route-paths.ts';
import {parseLocalSession} from '../../packages/client-core/src/local-session.ts';
import {parseRememberedServer} from '../../packages/client-core/src/remembered-server.ts';
import {serverPin, routeOrigin} from '../../packages/client-core/src/route-identity.ts';
import {setInstallationIdentity, validInstallationId} from '../../packages/client-core/src/installation.ts';
import {refreshStoredSession, type ConnectionEnvironment, type RefreshStoredOptions,
  type RememberedServer, type ServerConnectionStorage} from '../../packages/client-core/src/server-connections.ts';
import {parsePlaybackObservation} from '../../packages/client-core/src/playback-settings.ts';
import {parseStorageStatus} from '../../packages/client-core/src/storage-status.ts';

/** Single JS owner: CAS has no await between comparison and replacement. Bounded by core validation. */
export class MemoryMutationStore implements DurableUserMutationStore {
  private state = emptyUserMutationState();
  async read() {return structuredClone(this.state);}
  async compareAndSwap(revision: number, next: UserMutationState) {
    if (this.state.revision !== revision) return false;
    if (next.revision !== revision + 1) throw new Error('invalid_store_revision');
    this.state = structuredClone(validateUserMutationState(next)); return true;
  }
}
async function privateDirectory(path: string) {
  const stat = await lstat(path);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o700)
    throw new Error('mutation_directory_requires_owned_0700');
}
/** One exclusive driver owns this directory. A crash leaves the lock for operator review;
 * never remove it automatically or discard pending records to make a new run start. */
export async function lockMutationDirectory(path: string) {
  await privateDirectory(path);
  const lockPath = join(path, 'driver.lock');
  const lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
  await lock.writeFile(String(process.pid)); await lock.sync();
  return async () => {await lock.close(); await unlink(lockPath);};
}
/** Atomic, fsynced replacement under the driver directory lock. No tokens are stored. */
export class FileMutationStore implements DurableUserMutationStore {
  private queue: Promise<unknown> = Promise.resolve();
  constructor(private path: string) {}
  private async readFile() {
    let file;
    try {file = await open(this.path, constants.O_RDONLY | constants.O_NOFOLLOW);}
    catch (error) {if ((error as NodeJS.ErrnoException).code === 'ENOENT') return emptyUserMutationState(); throw error;}
    try {
      const stat = await file.stat();
      if (!stat.isFile() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o600 || stat.size > USER_MUTATION_MAX_BYTES)
        throw new Error('invalid_mutation_store_file');
      return structuredClone(validateUserMutationState(JSON.parse(await file.readFile('utf8'))));
    } finally {await file.close();}
  }
  async read() {await this.queue; return this.readFile();}
  compareAndSwap(revision: number, next: UserMutationState): Promise<boolean> {
    const captured = structuredClone(validateUserMutationState(next));
    const operation = this.queue.then(async () => {
      if ((await this.readFile()).revision !== revision) return false;
      if (captured.revision !== revision + 1) throw new Error('invalid_store_revision');
      const temporary = this.path + '.' + randomUUID() + '.tmp';
      const file = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
      try {
        try {await file.writeFile(JSON.stringify(captured)); await file.sync();} finally {await file.close();}
        await rename(temporary, this.path);
        const directory = await open(join(this.path, '..'), constants.O_RDONLY | constants.O_DIRECTORY);
        try {await directory.sync();} finally {await directory.close();}
        return true;
      } finally {await unlink(temporary).catch(error => {if (error.code !== 'ENOENT') throw error;});}
    });
    this.queue = operation.catch(() => {}); return operation;
  }
}
export async function mutationClient(target: string, session: LocalSession, store: DurableUserMutationStore, transport: typeof fetch) {
  const api = new HttpLocalApi(target, session.accessToken, transport);
  api.setUserMutationSession(session);
  // Do not silently replay old work as part of a fresh measured workload. Operator recovery is separate.
  if ((await store.read()).entries.length) throw new Error('pending_mutations_require_operator_recovery');
  const coordinator = api.configureUserMutations(store);
  try {await api.awaitUserMutationsReady();} catch (error) {coordinator.dispose(); throw error;}
  const http: V1Http = {send: request => api.requestRaw(request.path, request.method, request),
    acknowledgeUserMutation: intent => api.acknowledgeUserMutation(intent)};
  return {api, coordinator, sessions: new SessionsClient(http), http};
}
/** Caller identity remains fixed across ambiguity. Only parsed completion is acknowledged. */
export async function savedBatch(api: HttpLocalApi, serverId: string, operationId: string, items: readonly PersonalBatchItem[], signal?: AbortSignal) {
  const raw = await api.request<unknown>(PERSONAL_BATCH_PATH, 'PUT', personalBatchBody(operationId, items), signal);
  const entry = await api.getUserMutationCoordinator()?.inspectIntent(operationId);
  if (!entry || entry.state !== 'confirmed' || !entry.receipt) throw new Error('saved_receipt_unconfirmed');
  const receipt = parseUserMutationReceipt(entry.receipt, entry);
  if (receipt.status !== 'applied') throw new Error('saved_receipt_failed');
  let updated: number | undefined;
  if (raw && typeof raw === 'object' && 'mutationReceipt' in raw) {
    // A recovered tiny receipt proves the operation, never historical per-item counts.
    const marker = parseUserMutationReceipt(raw.mutationReceipt, entry);
    if (JSON.stringify(marker) !== JSON.stringify(receipt)) throw new Error('saved_recovery_mismatch');
  } else {
    const batch = parsePersonalBatchReceipt(raw, serverId, items.map(item => item.itemId));
    if (batch.operationId !== operationId || batch.failed || batch.updated !== items.length) throw new Error('saved_receipt_failed');
    updated = batch.updated;
  }
  await api.acknowledgeUserMutation(operationId);
  return {updated, recovered: updated === undefined};
}
/** Freeze the first final position for this exact generation, including a failed retry. */
export function finalStop(sessions: SessionsClient, id: string, generation: number) {
  let position: number | undefined, accepted: Promise<void> | undefined;
  return (positionMs?: number, signal?: AbortSignal) => {
    position ??= Math.max(0, Math.round(positionMs ?? 0));
    if (accepted) return accepted;
    const attempt = sessions.stop(id, position, signal, generation);
    accepted = attempt;
    void attempt.catch(() => {if (accepted === attempt) accepted = undefined;});
    return attempt;
  };
}

// ── Real refresh/storage/readback adapters (O-102) ────────────────────────────
// The canonical refresh chain is `refreshStoredSession` over a `ServerConnectionStorage` with the
// app's own process-global `installationIdentity()`. One driver process cannot hold 102 different
// installations at once, so every rotation runs in a one-shot child process for exactly one
// installation. The parent never swaps the global; the child sets it once and exits.
const CONNECTION_MAX_BYTES = 512 * 1024, REFRESH_REQUEST_MAX_BYTES = 64 * 1024, REFRESH_CHILD_TIMEOUT_MS = 30_000;

function damaged(): Error {return Object.assign(new Error('invalid_saved_connection'), {code: 'invalid_saved_connection'});}

/** An owned, non-symlink 0700 directory. Credential files never live outside one. */
export async function privateStoreDirectory(path: string) {
  const stat = await lstat(path);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o700)
    throw new Error('connection_directory_requires_owned_0700');
}
async function syncDirectory(path: string) {
  const directory = await open(path, constants.O_RDONLY | constants.O_DIRECTORY);
  try {await directory.sync();} finally {await directory.close();}
}
async function privateFile(path: string, maximum: number, code: string) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const stat = await file.stat();
    if (!stat.isFile() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o600 || stat.size > maximum) throw damaged();
    return file.readFile('utf8');
  } finally {await file.close();}
}

/** Canonical `ServerConnectionStorage` over one private 0600 file: the same record, damage and
 * atomic-replacement contract the apps use, without their platform keystore. A record that can
 * never be read again is reported `invalid_saved_connection`; a read that failed for now (locked,
 * busy, permission) is rethrown unchanged so refresh keeps the credential and retries. */
export class FileConnectionStore implements ServerConnectionStorage {
  private queue: Promise<unknown> = Promise.resolve();
  constructor(private path: string) {}
  private async load(): Promise<RememberedServer | undefined> {
    let text: string;
    try {text = await privateFile(this.path, CONNECTION_MAX_BYTES, 'invalid_saved_connection');}
    catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') return undefined;
      throw error;
    }
    let raw: unknown;
    try {raw = JSON.parse(text);} catch {throw damaged();}
    return parseRememberedServer(raw);
  }
  async read() {await this.queue; return this.load();}
  change(update: (current: RememberedServer | undefined) => RememberedServer | undefined, beforeCommit?: () => void): Promise<void> {
    const operation = this.queue.then(async () => {
      const current = await this.load();
      const next = update(current);
      if (next === current) return;
      // The guard runs under this store's lock immediately before the mutation; a failed guard writes nothing.
      beforeCommit?.();
      if (next === undefined) {
        await unlink(this.path).catch(error => {if (error.code !== 'ENOENT') throw error;});
        await syncDirectory(join(this.path, '..'));
        return;
      }
      const temporary = this.path + '.' + randomUUID() + '.tmp';
      const file = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
      try {
        try {await file.writeFile(JSON.stringify(next)); await file.sync();} finally {await file.close();}
        await rename(temporary, this.path);
        await syncDirectory(join(this.path, '..'));
      } catch (error) {await unlink(temporary).catch(() => {}); throw error;}
    });
    this.queue = operation.catch(() => {}); return operation;
  }
}

export type ServerIdentity = Readonly<{publicKey: string; fingerprint: string}>;

/** Store one manifest session in the canonical record shape, then read it back.
 * The pin is recomputed from the captured server identity (`serverPin`), so a mismatched
 * `serverId`/fingerprint is refused here rather than trusted from the manifest. */
export async function seedConnection(store: FileConnectionStore, target: string, session: LocalSession, identity: ServerIdentity) {
  const origin = routeOrigin(target);
  const pin = await serverPin(session.viewer.serverId, identity.publicKey, identity.fingerprint);
  if (session.serverIdentity && (session.serverIdentity.publicKey !== pin.publicKey || session.serverIdentity.fingerprint !== pin.fingerprint))
    throw new Error('manifest_server_identity_mismatch');
  const record: RememberedServer = {version: '1', logicalOrigin: origin, session: parseLocalSession(session, {}, {stored: true}), pin,
    routes: {paired: [origin]}};
  let existing: RememberedServer | undefined;
  await store.change(current => {
    existing = current;
    // A leftover record from another family is operator work, never silently replaced.
    if (current && (current.pin.serverId !== pin.serverId || current.session.sessionFamilyId !== record.session.sessionFamilyId))
      throw new Error('connection_store_holds_another_family');
    return current ?? record;
  });
  const saved = parseRememberedServer(await store.read());
  if (!saved || saved.pin.serverId !== pin.serverId || saved.pin.fingerprint !== pin.fingerprint
    || saved.session.sessionFamilyId !== record.session.sessionFamilyId || saved.session.viewer.accountId !== record.session.viewer.accountId
    || saved.session.viewer.profileId !== record.session.viewer.profileId || saved.session.installationId !== record.session.installationId)
    throw new Error('connection_store_readback_mismatch');
  return pin;
}

export type RefreshChildRequest = Readonly<{target: string; store: string; serverId: string; family: string;
  installationId: string; rejected?: string}>;

function transportFor(target: string): typeof fetch {
  const origin = new URL(target).origin;
  return async (input, init = {}) => {
    const url = new URL(String(input));
    if (url.origin !== origin || url.username || url.password) throw new Error('refresh_cross_origin_refused');
    return fetch(input, {...init, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer'});
  };
}

/** One rotation, isolated: the child sets exactly one installation identity, runs the canonical
 * `refreshStoredSession`, and exits. Nothing but the numeric exit status crosses back. */
async function refreshInChild(request: RefreshChildRequest): Promise<void> {
  request = {...request, store: resolve(request.store)};
  const requestPath = join(request.store + '.request.' + randomUUID());
  const file = await open(requestPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
  try {await file.writeFile(JSON.stringify(request)); await file.sync();} finally {await file.close();}
  try {
    // Only flags this module needs: an inherited `-e`/`--eval` or test flag would replace the child's entrypoint.
    const execArgs = process.execArgv.filter(arg => /^--(?:experimental-transform-types|experimental-strip-types|no-warnings|enable-source-maps)$/.test(arg));
    const script = fileURLToPath(new URL('./load-adapters.ts', import.meta.url));
    const child = spawn(process.execPath, [...execArgs, script, '--refresh-child', requestPath],
      {stdio: ['ignore', 'ignore', 'ignore'], env: {}, cwd: '/', timeout: REFRESH_CHILD_TIMEOUT_MS, killSignal: 'SIGKILL'});
    await new Promise<void>((resolve, reject) => {
      child.once('error', () => reject(new Error('refresh_child_spawn_failed')));
      child.once('exit', (code, signal) => code === 0 ? resolve()
        : reject(new Error(signal ? 'refresh_child_killed' : 'refresh_child_refused')));
    });
  } finally {await unlink(requestPath).catch(() => {});}
}

export type RefreshBrokerOptions = Readonly<{target: string; directory: string; storePath: string;
  serverId: string; family: string; installationId: string}>;

/** The `refresh` seam of `LocalSessionRefresher`: rotates in an isolated child, then reads the
 * renewal back out of the canonical store. Serial per identity; the refreshed session comes from
 * validated storage, never from a claimed child response. */
export function refreshBroker(options: RefreshBrokerOptions) {
  const store = new FileConnectionStore(options.storePath);
  let queue: Promise<unknown> = Promise.resolve();
  return (request: RefreshStoredOptions): Promise<LocalSession> => {
    const run = queue.then(async () => {
      const before = await store.read();
      if (!before) throw Object.assign(new Error('connection_store_missing'), {code: 'session_replaced'});
      // The caller's token is what must change; the child only force-renews when that token is
      // the one stored (a genuine 401 on the current credential).
      const caller = request.rejected;
      const rejected = caller && caller === before.session.accessToken ? caller : undefined;
      await refreshInChild({target: options.target, store: options.storePath, serverId: options.serverId,
        family: options.family, installationId: options.installationId, ...(rejected ? {rejected} : {})});
      const after = await store.read();
      if (!after || after.session.sessionFamilyId !== options.family || after.pin.serverId !== options.serverId)
        throw Object.assign(new Error('refresh_readback_missing'), {code: 'session_replaced'});
      // Success is "the caller now has a different, validated access token". A store that another
      // run already rotated is adopted, never mistaken for a failed rotation.
      if (caller ? after.session.accessToken === caller : after.session.accessToken === before.session.accessToken)
        throw new Error('refresh_did_not_rotate');
      return after.session;
    });
    queue = run.catch(() => {});
    return run;
  };
}

/** Child entrypoint body (`load-adapters.ts --refresh-child <request>`). */
export async function refreshChild(argv: readonly string[]) {
  const [requestPath, ...rest] = argv;
  if (!requestPath || rest.length) throw new Error('refresh_child_requires_one_request');
  let request: RefreshChildRequest;
  try {request = JSON.parse(await privateFile(requestPath, REFRESH_REQUEST_MAX_BYTES, 'invalid_request'));}
  catch {throw new Error('refresh_request_unreadable');}
  if (typeof request.target !== 'string' || typeof request.store !== 'string' || typeof request.serverId !== 'string'
    || typeof request.family !== 'string' || !validInstallationId(request.installationId)) throw new Error('refresh_request_invalid');
  new URL(request.target);
  const store = new FileConnectionStore(request.store);
  const record = parseRememberedServer(await store.read());
  if (!record || record.session.sessionFamilyId !== request.family || record.pin.serverId !== request.serverId)
    throw new Error('refresh_family_missing');
  // The family is bound to the installation that signed it in; a different claim is never sent.
  if (record.session.installationId !== request.installationId) throw new Error('refresh_installation_mismatch');
  setInstallationIdentity({installationId: request.installationId, name: 'Portico load driver', platform: 'linux', app: 'portico-load', appVersion: '1'});
  const transport = transportFor(request.target);
  const env: ConnectionEnvironment = {storage: store, fetcher: transport};
  const api = new HttpLocalApi(new URL(request.target).origin, record.session.accessToken, transport);
  await refreshStoredSession({env, api, family: {sessionFamilyId: request.family, serverId: request.serverId},
    ...(request.rejected ? {rejected: request.rejected} : {}), timeoutMs: 15_000});
  const next = parseRememberedServer(await store.read());
  if (!next || next.session.sessionFamilyId !== request.family) throw new Error('refresh_readback_missing');
}

/** Actual server readback before a measured window: the owner's live playback diagnostics and
 * storage status, never the manifest's claim about them. Raw absolute paths stay private; only
 * derived booleans and byte counts leave this adapter. */
export type ServerStateReadback = Readonly<{directConfigured: boolean; conversionsConfigured: boolean;
  databaseUnderExpectedRoot: boolean | null; reclaimableBytes: number}>;
export async function readServerState(api: Pick<HttpLocalApi, 'request'>, scope: Readonly<{serverId: string; appdataRoot?: string}>): Promise<ServerStateReadback> {
  const raw = await api.request<unknown>(routePath('get_admin_playback_diagnostics'));
  if (!raw || typeof raw !== 'object' || !('scope' in raw)) throw new Error('playback_diagnostics_unreadable');
  const declared = (raw as {scope: {serverId?: unknown}}).scope;
  if (declared.serverId !== scope.serverId) throw new Error('playback_diagnostics_scope_mismatch');
  const playback = parsePlaybackObservation(raw);
  const storage = parseStorageStatus(await api.request<unknown>(routePath('get_storage_status')));
  let databaseUnderExpectedRoot: boolean | null = null;
  if (scope.appdataRoot) {
    let root: string;
    try {root = await realpath(scope.appdataRoot);} catch {throw new Error('appdata_root_missing');}
    const database = storage.databasePath;
    databaseUnderExpectedRoot = database === root || database.startsWith(root.endsWith('/') ? root : root + '/');
  }
  return {directConfigured: playback.diagnostics.directConfigured, conversionsConfigured: playback.diagnostics.hlsConfigured,
    databaseUnderExpectedRoot, reclaimableBytes: storage.reclaimableBytes};
}

/** Operator-written appdata preparation receipt (`provision-scale.py bootstrap`). It proves a real
 * start/stop and a terminal real scan; the manifest's `stateAdapterReceipt` must name it exactly. */
export type PrepReceipt = Readonly<{receipt: string; serverId?: string; playableItems: number;
  mediaMinDurationSeconds: number; mediaDurationSource: 'probe' | 'declared'; databaseUnderAppdata: boolean;
  scanTerminal: boolean; stopped: boolean}>;
export async function readPrepReceipt(appdataRoot: string): Promise<PrepReceipt> {
  const path = join(appdataRoot, 'prep-receipt.json');
  let raw: any;
  try {raw = JSON.parse(await privateFile(path, 1024 * 1024, 'invalid_prep_receipt'));}
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') throw new Error('appdata_prep_receipt_missing');
    throw error;
  }
  const text = (v: unknown) => typeof v === 'string' && /^[A-Za-z0-9_.:-]{1,256}$/.test(v);
  const count = (v: unknown) => Number.isSafeInteger(v) && Number(v) >= 0;
  const playable = raw?.playable, bootstrap = raw?.bootstrap, scan = raw?.scan, storage = raw?.storage;
  if (!raw || raw.version !== 1 || raw.kind !== 'portico-appdata' || !text(raw.receipt) || raw.blocker
    || !bootstrap || bootstrap.stopped !== true || !scan || scan.terminal !== true
    || !playable || !count(playable.items) || !playable.items || !count(playable.minDurationSeconds)
    || !['probe', 'declared'].includes(playable.durationSource) || !storage || storage.databaseUnderAppdata !== true)
    throw new Error('appdata_prep_receipt_invalid');
  return {receipt: raw.receipt, ...(text(raw.serverId) ? {serverId: raw.serverId} : {}), playableItems: playable.items,
    mediaMinDurationSeconds: playable.minDurationSeconds, mediaDurationSource: playable.durationSource,
    databaseUnderAppdata: true, scanTerminal: true, stopped: true};
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = process.argv.slice(2);
  if (args[0] !== '--refresh-child') {
    console.error('load-adapters.ts is a module; its only entrypoint is --refresh-child.');
    process.exitCode = 2;
  } else {
    refreshChild(args.slice(1)).then(() => {process.exitCode = 0;},
      // One line, no error text: the parent never needs, and never prints, a private cause.
      () => {process.exitCode = 1;});
  }
}
