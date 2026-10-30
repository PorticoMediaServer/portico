import {routeBody} from "../../../contracts/src/routes.ts";
import {routePath} from "../../../contracts/src/route-paths.ts";
import type {ConsoleClient, RuntimeSettings, SettingsDocument} from '../console.ts';
import {parseConnectivityStatus, parseLogSettings, type ConnectivityPolicy, type ConnectivityStatus, type LogSettings, type LogDetailMinutes} from '../server-administration.ts';
import {parseDVRDocument, parseEnvelope, parseLiveDefaultsDocument, folderTemplateProblems, type DVRDefaults, type DVRDocument, type LiveDefaults} from '../administration.ts';
import {loadRemoteAccess, saveRemoteAccess, type RemoteAccess, type RemoteConfiguration} from '../remote-access.ts';
import {loadCertificate, saveCertificate, type CertificateApi, type CertificateConfig, type CertificateStatus} from '../certificates.ts';
import {certificateClaimed} from './secure-connection.ts';
import type {HttpLocalApi} from '../index.ts';
import {DraftForm, type Loaded} from './draft-form.ts';
import {UserMutationError, sameUserMutationScope, type UserMutationScope} from '../user-mutations.ts';

/**
 * The server's settings documents as draft forms (`draft-form.ts`), one per document, for the
 * Server pages of Settings. Every read, write, revision fence and operation id is here, so the
 * web and the iPhone app share them and a page has one Save.
 *
 * A form's value is what the owner edits; `status` is what arrives with it and is only shown
 * (effective values, choices the server offers, the revision to save against).
 */

export type ServerFormId = 'runtime' | 'connectivity' | 'remote' | 'certificate' | 'alerts' | 'liveDefaults' | 'recording' | 'maintenance' | 'logs';

export type ServerFormDeps = Readonly<{
  api: Pick<HttpLocalApi, 'request'> & Partial<Pick<HttpLocalApi, 'acknowledgeUserMutation' | 'awaitUserMutationsReady' | 'getUserMutationCoordinator' | 'getUserMutationScope'>>;
  console: ConsoleClient;
  /** The server every administration answer must name. */
  serverId: string;
  /** 48 lowercase hex characters (every write route accepts that shape). */
  operationId: () => string;
}>;

/** BE-API-10: one operation id per logical write. A retry of the same payload reuses it, so the server replays its receipt; a changed payload gets a new one. */
export function createOperationIds(make: () => string): {forPayload: (key: string) => string; release: () => void} {
  let current: {key: string; id: string} | null = null;
  return {
    forPayload: key => {
      if (current && current.key === key) return current.id;
      current = {key, id: make()};
      return current.id;
    },
    release: () => { current = null; },
  };
}

/** A read that answers 404 is a feature this server has not set up: the form is unavailable, not failed. */
async function orAbsent<T>(read: Promise<T>): Promise<T | null> {
  try { return await read; } catch (error) { if ((error as {status?: number} | null)?.status === 404) return null; throw error; }
}

// ── Runtime settings: name, converting video, capacity, how long records are kept ───────────

export type RuntimeStatus = Readonly<{revision: number; effective: RuntimeSettings; restartFields: readonly string[]; /** Whether the owner's own FFmpeg build passed the server's checks, when one is named. */ ffmpegValidation?: SettingsDocument['ffmpegValidation']}>;
const runtimeLoaded = (doc: SettingsDocument): Loaded<RuntimeSettings, RuntimeStatus> => ({value: doc.requested, status: {revision: doc.revision, effective: doc.effective, restartFields: doc.restartFields, ...(doc.ffmpegValidation ? {ffmpegValidation: doc.ffmpegValidation} : {})}});

/** The retention the server accepts: diagnostics and finished tasks 1–30 days, notifications 1–180. */
export const RETENTION_LIMITS: Readonly<Record<'diagnosticDays' | 'notificationDays' | 'jobDays', number>> = {diagnosticDays: 30, notificationDays: 180, jobDays: 30};

/** The server's own name rules (`operations/settings.go`: `SafeText(name, 100)` then no newline
 * or tab): nonblank, at most 100 UTF-8 bytes, no control characters and no bidi formatting. */
function serverNameProblem(name: string): string | undefined {
  if (!name.trim()) return 'server.form.nameRequired';
  if (new TextEncoder().encode(name).length > 100) return 'error.invalidInput';
  for (const character of name) {
    const code = character.codePointAt(0)!;
    if (code < 0x20 || code >= 0x7f && code <= 0x9f || code >= 0x202a && code <= 0x202e || code >= 0x2066 && code <= 0x2069) return 'error.invalidInput';
  }
  return undefined;
}

export function runtimeForm(deps: ServerFormDeps): DraftForm<RuntimeSettings, RuntimeStatus> {
  return new DraftForm<RuntimeSettings, RuntimeStatus>({
    load: async () => runtimeLoaded(await deps.console.settings()),
    save: async (draft, saved, status) => runtimeLoaded(await deps.console.applySettings(status?.revision ?? 0, draft, saved)),
    validate: draft => serverNameProblem(draft.name),
  });
}

// ── Connectivity: who may sign in from outside, streaming limits, home networks ──────────────

export type ConnectivityDraft = Omit<ConnectivityPolicy, 'revision'>;
const connectivityLoaded = (status: ConnectivityStatus): Loaded<ConnectivityDraft, ConnectivityStatus> => {
  const {revision: _revision, ...draft} = status.policy;
  return {value: draft, status};
};

export function connectivityForm(deps: ServerFormDeps): DraftForm<ConnectivityDraft, ConnectivityStatus> {
  const ids = createOperationIds(deps.operationId);
  return new DraftForm<ConnectivityDraft, ConnectivityStatus>({
    load: async signal => connectivityLoaded(parseConnectivityStatus(await deps.api.request(routePath("get_connectivity_status"), 'GET', undefined, signal))),
    save: async (draft, saved, status) => {
      // Only what changed is sent: the policy is a merge patch.
      const change: Record<string, unknown> = {};
      for (const key of Object.keys(draft) as (keyof ConnectivityDraft)[]) if (JSON.stringify(draft[key]) !== JSON.stringify(saved[key])) change[key] = draft[key];
      const revision = status?.policy.revision ?? 0;
      await deps.api.request(routePath("change_connectivity_policy"), 'PATCH', routeBody("change_connectivity_policy", {expectedRevision: revision, operationId: ids.forPayload(JSON.stringify({revision, change})), ...change}));
      ids.release();
    },
  });
}

// ── Remote access (the router and the public address) and the HTTPS certificate ─────────────

export function remoteAccessForm(deps: ServerFormDeps): DraftForm<RemoteConfiguration, RemoteAccess> {
  const api = deps.api as HttpLocalApi;
  return new DraftForm<RemoteConfiguration, RemoteAccess>({
    load: async signal => { const status = await orAbsent(loadRemoteAccess(api, signal)); return status && {value: status.config, status}; },
    save: async (draft, _saved, status) => { const next = await saveRemoteAccess(api, status!, draft); return {value: next.config, status: next}; },
  });
}

/**
 * The certificate settings, plus `claimed`: whether the server has a Portico Account connection
 * for the certificate service to work with. It is read-only (never sent back) and lets the
 * Secure connection switch appear only once there is something to switch.
 */
export type CertificateDraft = CertificateConfig & Readonly<{claimed: boolean}>;

export function certificateForm(deps: ServerFormDeps): DraftForm<CertificateDraft, CertificateStatus> {
  const api: CertificateApi = {requestCertificate: (action, body, signal) => (action === 'status' ? deps.api.request(routePath("get_networking_certificate"), 'GET', undefined, signal) : deps.api.request(routePath(action==='config'?'post_networking_certificate_config':'post_networking_certificate_retry'), 'POST', body, signal))};
  const bounded = () => { const c = new AbortController(); setTimeout(() => c.abort(), 15_000); return c.signal; };
  const draft = (status: CertificateStatus): CertificateDraft => ({...status.config, claimed: certificateClaimed(status)});
  return new DraftForm<CertificateDraft, CertificateStatus>({
    load: async signal => { const status = await orAbsent(loadCertificate(api, signal)); return status && {value: draft(status), status}; },
    save: async (value, _saved, status) => { const next = await saveCertificate(api, status!, value, bounded()); return {value: draft(next), status: next}; },
  });
}

// ── Alert thresholds ─────────────────────────────────────────────────────────────────────────

/** Who receives each server notice: the owner and the administrators, the owner alone, or no one. */
export type NoticeAudience = 'both' | 'owner' | 'off';
export const noticeKinds = ['scan', 'storage', 'certificate', 'recording', 'feedback'] as const;
export type AlertThresholds = Readonly<{storageWarningPercent: number; storageCriticalPercent: number; certificateWarningDays: number; noticeAudience?: Readonly<Record<typeof noticeKinds[number], NoticeAudience>>}>;

const noticeAudienceValue = (v: unknown): v is NoticeAudience => v === 'both' || v === 'owner' || v === 'off';
/** Every kind with a known value, or nothing: a server that does not route notices offers no row for it. */
function readNoticeAudience(raw: unknown): AlertThresholds['noticeAudience'] {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const v = raw as Record<string, unknown>;
  return noticeKinds.every(kind => noticeAudienceValue(v[kind])) ? Object.freeze(Object.fromEntries(noticeKinds.map(kind => [kind, v[kind]]))) as AlertThresholds['noticeAudience'] : undefined;
}

export function alertsForm(deps: ServerFormDeps): DraftForm<AlertThresholds, {revision: number}> {
  const read = (raw: unknown): Loaded<AlertThresholds, {revision: number}> => {
    const v = ((raw as {data?: unknown})?.data ?? raw) as AlertThresholds & {revision: number; noticeAudience?: unknown};
    if (!v || typeof v.revision !== 'number' || typeof v.storageWarningPercent !== 'number') throw new Error('The server returned alert settings this version cannot read.');
    const noticeAudience = readNoticeAudience(v.noticeAudience);
    return {value: {storageWarningPercent: v.storageWarningPercent, storageCriticalPercent: v.storageCriticalPercent, certificateWarningDays: v.certificateWarningDays, ...(noticeAudience ? {noticeAudience} : {})}, status: {revision: v.revision}};
  };
  return new DraftForm<AlertThresholds, {revision: number}>({
    load: async signal => read(await deps.api.request(routePath("get_admin_notifications_settings"), 'GET', undefined, signal)),
    save: async (draft, _saved, status) => { await deps.api.request(routePath("patch_admin_notifications_settings"), 'PATCH', routeBody("patch_admin_notifications_settings", {expectedRevision: status?.revision ?? 0, ...draft})); },
    // Free space: the critical threshold is the lower one (5 % beneath a 10 % warning).
    validate: draft => (draft.storageCriticalPercent >= draft.storageWarningPercent ? 'server.form.alertOrder' : undefined),
  });
}

// ── Live TV and recording defaults ──────────────────────────────────────────────────────────

/** A failed parse or durable acknowledgement keeps the original ID, body and
 * revision. A changed draft must be reviewed before it can become another write. */
function settingsSave<D>(deps: ServerFormDeps, path: string, method: string, parse: (raw: unknown) => D) {
  let pending: {payload: string; body: string; operationId: string; dispatched: boolean; scope?: UserMutationScope; parsed?: {value: D}} | undefined;
  return async (payload: object): Promise<D> => {
    await deps.api.awaitUserMutationsReady?.();
    const scope = deps.api.getUserMutationScope?.(), coordinator = deps.api.getUserMutationCoordinator?.();
    if (pending?.scope && (!scope || !sameUserMutationScope(pending.scope, scope))) throw new UserMutationError('mutation_scope_changed');
    // Only an explicit later Save may release this opening's cache after root
    // review has removed the original durable command. Never clear on failure.
    if (pending?.dispatched && pending.scope && coordinator && !(await coordinator.inspectIntent(pending.operationId))) pending = undefined;
    const serialized = JSON.stringify(payload);
    if (pending && pending.payload !== serialized) throw new UserMutationError('mutation_needs_review');
    if (!pending) {
      const operationId = deps.operationId();
      pending = {payload: serialized, body: JSON.stringify({...JSON.parse(serialized), operationId}), operationId, dispatched: false, ...(scope ? {scope} : {})};
    }
    const original = pending;
    if (!original.parsed) {original.dispatched = true;original.parsed = {value: parse(await deps.api.request(path, method, JSON.parse(original.body)))};}
    await deps.api.acknowledgeUserMutation?.(original.operationId);
    pending = undefined;
    return original.parsed.value;
  };
}

function documentForm<T extends object, D extends {revision: number; settings: T}>(deps: ServerFormDeps, path: string, parse: (raw: unknown) => D, validate?: (draft: T, doc: D | undefined) => string | undefined): DraftForm<T, D> {
  const save = settingsSave(deps, path, 'PUT', raw => parseEnvelope(raw, deps.serverId, parse).result);
  return new DraftForm<T, D>({
    load: async signal => { const doc = await orAbsent(deps.api.request<unknown>(path, 'GET', undefined, signal).then(raw => parseEnvelope(raw, deps.serverId, parse).result)); return doc && {value: doc.settings, status: doc}; },
    save: async (draft, _saved, doc) => {
      const revision = doc?.revision ?? 0;
      const stored = await save({expectedRevision: revision, settings: draft});
      return {value: stored.settings, status: stored};
    },
    validate,
  });
}

export const liveDefaultsForm = (deps: ServerFormDeps) => documentForm<LiveDefaults, ReturnType<typeof parseLiveDefaultsDocument>>(deps, routePath("get_live_settings"), parseLiveDefaultsDocument);

export const recordingForm = (deps: ServerFormDeps) => documentForm<DVRDefaults, DVRDocument>(deps, routePath("get_admin_dvr_settings"), parseDVRDocument, (draft, doc) => {
  if (!doc?.folderTokens.length) return undefined;
  return [...folderTemplateProblems(draft.folderTemplate, doc.folderTokens), ...folderTemplateProblems(draft.movieFolderTemplate, doc.folderTokens)][0];
});

// ── Maintenance: windows, background priority, what is kept and for how long ────────────────

export type MaintenanceWindow = Readonly<{id: string; name: string; enabled: boolean; cadence: string; days: readonly string[]; startMinute: number; durationMinutes: number; timezone: string; tasks: readonly string[]}>;
export type MaintenanceSettings = Readonly<{windows: readonly MaintenanceWindow[]; retention: Readonly<Record<string, number>>; backupKeepCount: number; backgroundSpeed?: 'gentle' | 'normal' | 'fast'}>;
export type MaintenanceDocument = Readonly<{revision: number; pausedUntilUnixMillis: number; settings: MaintenanceSettings; cadences: readonly {id: string; name: string; days: readonly string[]; description: string}[]; tasks: readonly string[]; days: readonly string[]; retentionMaxima: Readonly<Record<string, number>>; storageCategories: readonly {id: string; name: string; retentionKey: string}[]}>;

/** The document is owner configuration the server validates in full on save, so it is read structurally. */
export function parseMaintenanceDocument(raw: unknown): MaintenanceDocument {
  const v = raw as MaintenanceDocument;
  if (!v || typeof v.revision !== 'number' || !v.settings || !Array.isArray(v.settings.windows) || !Array.isArray(v.cadences) || !Array.isArray(v.tasks)) throw new Error('The server returned maintenance settings this version cannot read.');
  return {...v, pausedUntilUnixMillis: Number.isFinite(v.pausedUntilUnixMillis) ? v.pausedUntilUnixMillis : 0, settings: {...v.settings, backgroundSpeed:v.settings.backgroundSpeed==='gentle'||v.settings.backgroundSpeed==='fast'?v.settings.backgroundSpeed:'normal'}};
}

export const maintenanceForm = (deps: ServerFormDeps) => documentForm<MaintenanceSettings, MaintenanceDocument>(deps, routePath("get_admin_maintenance_settings"), parseMaintenanceDocument);

/** Reuse operationId when retrying the same pause/resume action. */
export async function pauseBackgroundWork(deps: Pick<ServerFormDeps, 'api'>, paused: boolean, operationId: string): Promise<MaintenanceDocument> {
  return parseMaintenanceDocument(await deps.api.request(routePath("post_admin_background_pause"), 'POST', routeBody("post_admin_background_pause", {paused, operationId})));
}

export type ResourceUsage = Readonly<{connections: {ordinary: number; total: number; ordinaryLimit: number; consoleReserve: number} | null; analysis: {trackedBytes: number; limitBytes: number; measured: boolean}; localArtwork: {trackedBytes: number; limitBytes: number; measured: boolean}; streams: number; streamLimit: number | null; subtitleTracksPerFile: number; subtitleFileLimitMiB: number}>;
export async function loadResourceUsage(api: Pick<HttpLocalApi, 'request'>, signal?: AbortSignal): Promise<ResourceUsage> {
  const value = await api.request<ResourceUsage>(routePath("get_admin_resource_usage"), 'GET', undefined, signal);
  if (!value || !Number.isSafeInteger(value.streams) || !Number.isSafeInteger(value.analysis?.trackedBytes) || !Number.isSafeInteger(value.analysis?.limitBytes) || !Number.isSafeInteger(value.subtitleTracksPerFile) || !Number.isSafeInteger(value.localArtwork?.trackedBytes) || !Number.isSafeInteger(value.localArtwork?.limitBytes) || typeof value.localArtwork?.measured !== 'boolean') throw new Error('The server returned resource usage this version cannot read.');
  return value;
}

export type ScheduledJob = Readonly<{task: string; /** Names of the enabled windows that run it. */ windows: readonly string[]; /** The start of the next window that runs it, in the server window's own clock, or undefined when no enabled window does. */ nextAt?: Date}>;

const DAY_INDEX: Readonly<Record<string, number>> = {sunday: 0, monday: 1, tuesday: 2, wednesday: 3, thursday: 4, friday: 5, saturday: 6, sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6};

/** When a window next opens, by this device's clock (the document's window times are the server's local times; the two agree when owner and server share a time zone, which the row says). */
export function nextWindowStart(window: MaintenanceWindow, now: Date): Date | undefined {
  if (!window.enabled) return undefined;
  const days = new Set(window.days.map(d => DAY_INDEX[d.toLowerCase()]).filter(d => d !== undefined));
  for (let offset = 0; offset < 8; offset++) {
    const at = new Date(now.getFullYear(), now.getMonth(), now.getDate() + offset, Math.floor(window.startMinute / 60), window.startMinute % 60);
    if ((days.size === 0 || days.has(at.getDay())) && at.getTime() > now.getTime()) return at;
  }
  return undefined;
}

/** Every job the server runs on a schedule, with the windows it runs in and when the next one opens. */
export function scheduledJobs(doc: Pick<MaintenanceDocument, 'tasks'>, settings: MaintenanceSettings, now: Date): readonly ScheduledJob[] {
  return doc.tasks.map(task => {
    const running = settings.windows.filter(w => w.enabled && w.tasks.includes(task));
    const starts = running.map(w => nextWindowStart(w, now)).filter((d): d is Date => !!d).sort((a, b) => a.getTime() - b.getTime());
    return {task, windows: running.map(w => w.name), ...(starts[0] ? {nextAt: starts[0]} : {})};
  });
}

// ── Message log detail ──────────────────────────────────────────────────────────────────────

export type LogsDraft = Readonly<{logLevel: LogSettings['logLevel']; logDetailMinutes: LogDetailMinutes; retention: LogSettings['retention']}>;

export function logsForm(deps: ServerFormDeps): DraftForm<LogsDraft, LogSettings> {
  const save = settingsSave(deps, routePath('patch_admin_logs_settings'), 'PATCH', parseLogSettings);
  return new DraftForm<LogsDraft, LogSettings>({
    load: async signal => { const settings = parseLogSettings(await deps.api.request(routePath("get_admin_logs_settings"), 'GET', undefined, signal)); return {value: {logLevel: settings.logLevel, logDetailMinutes: settings.logDetailMinutes ?? 30, retention: settings.retention}, status: settings}; },
    save: async (draft, _saved, status) => {
      const payload = {logLevel: draft.logLevel, retention: draft.retention, ...(status?.logDetailMinutes === undefined ? {} : {logDetailMinutes: draft.logDetailMinutes})};
      const settings = await save({expectedRevision: status?.revision ?? 0, ...payload});
      return {value: {logLevel: settings.logLevel, logDetailMinutes: settings.logDetailMinutes ?? 30, retention: settings.retention}, status: settings};
    },
  });
}

/** "Record more detail for 30 minutes": on, or off again. The one control for extra detail. */
export async function setDetailWindow(deps: ServerFormDeps, on: boolean): Promise<void> {
  await deps.api.request(routePath("post_admin_logs_debug_window"), 'POST', routeBody("post_admin_logs_debug_window", {operationId: deps.operationId(), minutes: on ? 30 : 0}));
}

// ── The forms of a page ─────────────────────────────────────────────────────────────────────

const FACTORIES: Readonly<Record<ServerFormId, (deps: ServerFormDeps) => DraftForm<any, any>>> = {
  runtime: runtimeForm, connectivity: connectivityForm, remote: remoteAccessForm, certificate: certificateForm, alerts: alertsForm,
  liveDefaults: liveDefaultsForm, recording: recordingForm, maintenance: maintenanceForm, logs: logsForm,
};

export function createServerForm(id: ServerFormId, deps: ServerFormDeps): DraftForm<any, any> {
  return FACTORIES[id](deps);
}

/** Reads a dotted path from a draft (`keepPolicy.mode`). */
export function formValue(draft: unknown, path: string): unknown {
  // A setting whose own name has a dot in it ("library.missingTitles") is one key, not a path.
  if (draft !== null && typeof draft === 'object' && path in (draft as object)) return (draft as Record<string, unknown>)[path];
  let at: unknown = draft;
  for (const part of path.split('.')) {
    if (at === null || typeof at !== 'object') return undefined;
    at = (at as Record<string, unknown>)[part];
  }
  return at;
}

/** The draft with a dotted path set, copying only the objects on the way. */
export function withFormValue<T extends object>(draft: T, path: string, value: unknown): T {
  if (path in draft) return {...draft, [path]: value};
  const [head, ...rest] = path.split('.');
  const current = (draft as Record<string, unknown>)[head!];
  return {...draft, [head!]: rest.length ? withFormValue((current ?? {}) as object, rest.join('.'), value) : value};
}

/** A list setting with one entry added: trimmed, never empty, never a repeat. */
export function withListEntry(list: readonly string[], typed: string): readonly string[] {
  const entry = typed.trim();
  return !entry || list.includes(entry) ? list : [...list, entry];
}
