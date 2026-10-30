import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
/**
 * A library's metadata source (`GET`/`PUT /v1/libraries/{id}/metadata/agent`),
 * Plex-agent style: `online` (Portico's online providers after the library's own files, the
 * default) or `local` (file and folder names, embedded tags, NFO and other sidecars, and local
 * artwork only; the server never goes online for the library). The server lists the choices for
 * the library's kind, the default first, with their names and descriptions.
 *
 * A change carries the revision it was read at. `metadata_conflict` (409) means someone changed it
 * meanwhile: the caller reloads and shows the current choice; it never retries blind.
 */
export type MetadataAgentId = 'online' | 'local' | (string & {});
export type MetadataAgentOption = Readonly<{id: MetadataAgentId; name: string; description: string; providers: readonly string[]; languages: readonly string[]; defaultLanguage?: string}>;
export type LibraryMetadataAgent = Readonly<{libraryId: string; libraryKind: string; revision: number; agent: MetadataAgentId; agents: readonly MetadataAgentOption[]; language?: string}>;

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
  });
}

/** Reads the server's answer; throws on one without what a change needs. */
export function parseMetadataAgent(raw: unknown): LibraryMetadataAgent {
  const o = raw as Record<string, unknown> | null;
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
  return Object.freeze({libraryId: text(o.libraryId, 128), libraryKind: text(o.libraryKind, 32), revision: o.revision, agent: o.agent as string, agents: Object.freeze(agents), ...(language ? {language} : {})});
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

export const metadataAgentPath = (libraryId: string) => encodedRoutePath("get_libraries_id_metadata_agent", {"id": encodeURIComponent(libraryId)});

export const kindMetadataAgentsPath = (kind: string) => encodedRoutePath("get_library_kinds_kind_metadata_agents", {"kind": encodeURIComponent(kind)});

/** The `PUT` body that chooses `agent` for the library as it was read. */
export const metadataAgentChange = (current: LibraryMetadataAgent, agent: MetadataAgentId) => ({expectedRevision: current.revision, agent});

type Request = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
/** Chooses the library's metadata source; resolves to the saved state. Rejects as the server answered (409 `metadata_conflict`, 400 `invalid_metadata_source`…). */
export async function saveMetadataAgent(request: Request, current: LibraryMetadataAgent, libraryId: string, agent: MetadataAgentId): Promise<LibraryMetadataAgent> {
  return parseMetadataAgent(await request<unknown>(metadataAgentPath(libraryId), 'PUT', metadataAgentChange(current, agent)));
}

/** Whether an error is the server's "someone changed this meanwhile" for the metadata source. */
export const isMetadataConflict = (e: unknown) => (e as {code?: unknown} | null)?.code === 'metadata_conflict' || ((e as {status?: unknown} | null)?.status === 409 && !(e as {code?: unknown} | null)?.code);
