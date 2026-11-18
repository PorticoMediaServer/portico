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
