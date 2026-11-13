import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
import {serverPath} from '../query/server-api.ts';
import type {LibraryContentApi} from '../library-content.ts';
import type {ErrorBody} from '../../../contracts/src/next/api.ts';
/**
 * The existing metadata-source controls consume this projection of the rebuilt admin
 * library read. Choices and availability come from the server's provider registration.
 * Changes PATCH the library with If-Match; a stale read is refused with 412.
 */
export type MetadataAgentId = 'online' | 'local' | (string & {});
export type MetadataAgentOption = Readonly<{id: MetadataAgentId; name: string; description: string; providers: readonly string[]; languages: readonly string[]; defaultLanguage?: string; available?: boolean; reason?: string}>;
export type LibraryMetadataAgent = Readonly<{libraryId: string; libraryKind: string; revision: number; agent: MetadataAgentId; agents: readonly MetadataAgentOption[]; language?: string; region?: string}>;

const text = (v: unknown, max: number) => (typeof v === 'string' && v.length <= max ? v : '');

/** The languages an agent can fetch metadata in: lowercase ISO 639-1 codes only. Missing or malformed → `[]`, never a throw. */
function parseLanguages(v: unknown): readonly string[] {
  if (!Array.isArray(v)) return Object.freeze([]);
  const out: string[] = [];
  for (const code of v) {
    if (out.length >= 64) break;
    if (typeof code === 'string' && /^[a-z]{2}$/.test(code)) out.push(code);
  }
  return Object.freeze(out);
}

function parseAgentOption(a: Record<string, unknown>): MetadataAgentOption | null {
  if (!a || typeof a !== 'object' || !text(a.id, 64)) return null;
  const languages = parseLanguages(a.languages);
  const def = typeof a.defaultLanguage === 'string' && languages.includes(a.defaultLanguage) ? (a.defaultLanguage as string) : undefined;
  return Object.freeze({
    id: a.id as string,
    name: text(a.name, 120) || (a.id as string),
    description: text(a.description, 600),
    providers: Object.freeze(Array.isArray(a.providers) ? a.providers.filter((p): p is string => typeof p === 'string' && p.length <= 32).slice(0, 16) : []),
    languages,
    ...(def ? {defaultLanguage: def} : {}),
    ...(typeof a.available === 'boolean' ? {available: a.available} : {}),
    ...(text(a.reason, 600) ? {reason: a.reason as string} : {}),
  });
}

/** Reads the server's answer; throws on one without what a change needs. */
export function parseMetadataAgent(raw: unknown): LibraryMetadataAgent {
  let o = raw as Record<string, unknown> | null;
  if (o && typeof o.metadataProvider === 'string') {
    if (!Array.isArray(o.metadataProviders) || !text(o.id, 128) || !text(o.kind, 32) || typeof o.revision !== 'string' || !/^[0-9]+$/.test(o.revision)) throw new Error('Invalid metadata source.');
    const kinds: Readonly<Record<string, string>> = {movies: 'movie', shows: 'tv', audiobooks: 'audiobook', books: 'book', comics: 'comic'};
    const names: Readonly<Record<string, string>> = {tmdb: 'TMDB', tvdb: 'TheTVDB', musicbrainz: 'MusicBrainz', anilist: 'AniList', local: 'Local only'};
    o = {...o, libraryId: o.id, libraryKind: kinds[String(o.kind)] ?? o.kind, revision: Number(o.revision), agent: o.metadataProvider,
      agents: o.metadataProviders.slice(0, 16).map((value: unknown) => {
        const p = value as Record<string, unknown> | null;
        if (!p || typeof p.id !== 'string' || typeof p.available !== 'boolean') throw new Error('Invalid metadata source.');
        return {...p, name: names[p.id] ?? p.id, description: p.available ? '' : text(p.reason, 600), providers: p.id === 'local' ? [] : [p.id]};
      })};
  }
  if (!o || typeof o !== 'object' || typeof o.revision !== 'number' || !Number.isSafeInteger(o.revision) || o.revision < 1 || !text(o.agent, 64) || !Array.isArray(o.agents)) throw new Error('Invalid metadata source.');
  const agents: MetadataAgentOption[] = [];
  for (const a of o.agents.slice(0, 16) as Record<string, unknown>[]) {
    if (!a || typeof a !== 'object') continue;
    const parsed = parseAgentOption(a);
    if (!parsed || agents.some(x => x.id === parsed.id)) continue;
    agents.push(parsed);
  }
  if (!agents.some(a => a.id === o.agent)) throw new Error('Invalid metadata source.');
  const language = typeof o.language === 'string' && o.language.length > 0 && o.language.length <= 35 ? (o.language as string) : undefined;
  return Object.freeze({libraryId: text(o.libraryId, 128), libraryKind: text(o.libraryKind, 32), revision: o.revision, agent: o.agent as string, agents: Object.freeze(agents), ...(language ? {language} : {}), ...(typeof o.region === 'string' ? {region: o.region} : {})});
}

/** The choices before a library exists (`GET /v1/library-kinds/{kind}/metadata-agents`). */
export type KindMetadataAgents = Readonly<{libraryKind: string; agents: readonly MetadataAgentOption[]}>;

/** Reads the kind's choices; throws only when `agents` isn't an array or is empty after parsing. */
export function parseKindMetadataAgents(raw: unknown): KindMetadataAgents {
  const o = raw as Record<string, unknown> | null;
  if (!o || typeof o !== 'object' || !Array.isArray(o.agents)) throw new Error('Invalid metadata choices.');
  const agents: MetadataAgentOption[] = [];
  for (const a of (o.agents as unknown[]).slice(0, 16)) {
    if (!a || typeof a !== 'object') continue;
    const parsed = parseAgentOption(a as Record<string, unknown>);
    if (!parsed || agents.some(x => x.id === parsed.id)) continue;
    agents.push(parsed);
  }
  if (!agents.length) throw new Error('Invalid metadata choices.');
  return Object.freeze({libraryKind: text(o.libraryKind, 32), agents: Object.freeze(agents)});
}

export const metadataAgentPath = (libraryId: string) => serverPath('GET /v1/admin/libraries/{id}', {id: libraryId});

export const kindMetadataAgentsPath = (kind: string) => encodedRoutePath("get_library_kinds_kind_metadata_agents", {"kind": encodeURIComponent(kind)});

/** A local command; the writer moves its revision to If-Match, never the server body. */
export const metadataAgentChange = (current: LibraryMetadataAgent, agent: MetadataAgentId, region?: string) => ({expectedRevision: current.revision, agent, ...(region === undefined ? {} : {region})});

export async function writeLibraryMetadataAgent<T = unknown>(api: LibraryContentApi, libraryId: string, raw: unknown, signal?: AbortSignal): Promise<T> {
  const body = raw as {expectedRevision?: number; agent?: string; region?: string} | null;
  if (!body || !Number.isSafeInteger(body.expectedRevision) || body.expectedRevision! < 1 || !text(body.agent, 64) || body.region !== undefined && typeof body.region !== 'string') throw new Error('Invalid metadata change.');
  if (!api.requestRaw) throw new Error('This app cannot send the library revision safely.');
  const response = await api.requestRaw(metadataAgentPath(libraryId), 'PATCH', {
    headers: {'If-Match': `"${body.expectedRevision}"`}, body: {metadataProvider: body.agent, ...(body.region === undefined ? {} : {region: body.region})}, signal, singleAttempt: true,
  });
  if (response.status >= 400) {
    const error = (response.body as ErrorBody | undefined)?.error;
    const field = Array.isArray(error?.fields) ? error.fields.find(p => p.path === 'metadataProvider' || p.path === 'region') : undefined;
    throw Object.assign(new Error(text(field?.message, 600) || text(error?.message, 600) || 'The library could not be changed.'), {status: response.status, code: error?.code ?? 'request_failed'});
  }
  return response.body as T;
}

type Request = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
/** Existing request-only callers remain accepted, but cannot write without a header-capable port. */
export async function saveMetadataAgent(request: Request | LibraryContentApi, current: LibraryMetadataAgent, libraryId: string, agent: MetadataAgentId): Promise<LibraryMetadataAgent> {
  const api = typeof request === 'function' ? {request} : request;
  return parseMetadataAgent(await writeLibraryMetadataAgent(api, libraryId, metadataAgentChange(current, agent)));
}

/** Whether an error is the server's "someone changed this meanwhile" for the metadata source. */
export const isMetadataConflict = (e: unknown) => (e as {status?: unknown} | null)?.status === 412 || (e as {code?: unknown} | null)?.code === 'metadata_conflict' || ((e as {status?: unknown} | null)?.status === 409 && !(e as {code?: unknown} | null)?.code);
