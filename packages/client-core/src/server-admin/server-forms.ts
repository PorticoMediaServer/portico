import {routeBody} from "../../../contracts/src/routes.ts";
import {routePath} from "../../../contracts/src/route-paths.ts";
import type {ConsoleClient, RuntimeSettings, SettingsDocument} from '../console.ts';
import {DraftForm, type Loaded} from './draft-form.ts';
import {callServer} from '../query/server-api.ts';
import {putSettingsDocument, type SettingsDocumentAPI} from '../settings-document.ts';

/**
 * The server's settings documents as draft forms (`draft-form.ts`), one per document, for the
 * Server pages of Settings. Every read, write, revision fence and operation id is here, so the
 * web and the iPhone app share them and a page has one Save.
 *
 * A form's value is what the owner edits; `status` is what arrives with it and is only shown
 * (effective values, choices the server offers, the revision to save against).
 */

/** Settings rows can refer to documents outside this module's draft factories. */
export type ServerFormId = 'runtime' | 'connectivity' | 'remote' | 'certificate' | 'alerts' | 'liveDefaults' | 'recording' | 'maintenance' | 'logs' | 'segments' | 'titleData' | 'streamLimits';

export type ServerFormDeps = Readonly<{
  api: SettingsDocumentAPI;
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

// ── Streams at once for each person ─────────────────────────────────────────────────────────

/** The owner's default number of streams at once for each person (definition 07 rule 36), from the stream limits
 * (`GET`/`PATCH /v1/admin/playback/stream-limits`). 0 is unlimited; a save names only this value. */
export function streamLimitsForm(deps: ServerFormDeps): DraftForm<Readonly<{perPerson: number}>, undefined> {
  const loaded = (limits: Readonly<{perPerson?: number}>) => ({value: {perPerson: limits.perPerson ?? 0}, status: undefined});
  return new DraftForm<Readonly<{perPerson: number}>, undefined>({
    load: async signal => loaded(await callServer(deps.api, 'GET /v1/admin/playback/stream-limits', {signal})),
    save: async draft => loaded(await callServer(deps.api, 'PATCH /v1/admin/playback/stream-limits', {body: {perPerson: draft.perPerson}})),
  });
}

// ── The forms of a page ─────────────────────────────────────────────────────────────────────

const FACTORIES = {
  runtime: runtimeForm, alerts: alertsForm,
  segments: deps => namedSettingsForm(deps, 'segments'),
  titleData: deps => namedSettingsForm(deps, 'title_data'),
  streamLimits: streamLimitsForm,
} satisfies Partial<Record<ServerFormId, (deps: ServerFormDeps) => DraftForm<any, any>>>;

export type DraftFormId = keyof typeof FACTORIES;

export function createServerForm(id: DraftFormId, deps: ServerFormDeps): DraftForm<any, any> {
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

/** A rebuilt settings document: carry its revision and send only the edited fields. */
function namedSettingsLoaded(raw: unknown): Loaded<Record<string, unknown>, {etag: string}> {
  const document = raw as {values?: unknown; etag?: unknown} | null;
  if (!document || typeof document.etag !== 'string' || !/^"[0-9]+"$/.test(document.etag) ||
    typeof document.values !== 'object' || document.values === null || Array.isArray(document.values)) throw Object.assign(new Error('invalid_settings_document'), {code: 'invalid_settings_document'});
  return {value: {...document.values as Record<string, unknown>}, status: {etag: document.etag}};
}
/** The same validated document for a view that offers no editor. */
export async function readNamedSettings(api: SettingsDocumentAPI, name: string, signal?: AbortSignal) {
  return namedSettingsLoaded(await callServer(api, 'GET /v1/admin/settings/{name}', {id: name, signal}));
}
export function namedSettingsForm(deps: ServerFormDeps, name: string): DraftForm<Record<string, unknown>, Record<string, unknown>> {
  return new DraftForm<Record<string, unknown>, Record<string, unknown>>({
    load: signal => orAbsent(readNamedSettings(deps.api, name, signal)),
    save: async (draft, saved, status) => {
      const change = Object.fromEntries(Object.entries(draft).filter(([key, value]) => JSON.stringify(value) !== JSON.stringify(saved[key])));
      return namedSettingsLoaded(await putSettingsDocument(deps.api, name, typeof status?.etag === 'string' ? status.etag : '', change));
    },
  });
}
