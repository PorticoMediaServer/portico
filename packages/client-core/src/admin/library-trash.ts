import {callServer, type ServerRequester} from '../query/server-api.ts';
import type {AdminLibrary, LibraryOptions} from '../../../contracts/src/next/api.ts';

/**
 * A library's missing titles: its trash (03-scanning rules 59 to 62). Each library has its own rule for emptying it:
 * after a number of days (1 to 3,650; 30 to start), after every scan, or only when the owner asks. Empty trash removes
 * them now, once the count is typed, and touches nothing on disk.
 */
export type TrashRule = Readonly<{mode: 'days' | 'scan' | 'manual'; days: number}>;
export type MissingTitle = Readonly<{id: string; title: string; year?: number; missingSince: string; removedAt?: string}>;
export type LibraryTrash = Readonly<{name: string; rule: TrashRule; options: LibraryOptions; items: readonly MissingTitle[]; total: number}>;

const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const invalid = () => Object.assign(new Error('invalid_library_trash'), {code: 'invalid_library_trash'});
const time = (v: unknown): v is string => typeof v === 'string' && Number.isFinite(Date.parse(v));

function ruleOf(options: unknown): TrashRule {
  const trash = object(options) && object(options.trash) ? options.trash : {};
  const mode = trash.mode === 'scan' || trash.mode === 'manual' ? trash.mode : 'days';
  const days = typeof trash.days === 'number' && Number.isInteger(trash.days) && trash.days >= 1 && trash.days <= 3650 ? trash.days : 30;
  return Object.freeze({mode, days});
}

/** The library's rule and its missing titles, read together. */
export async function readLibraryTrash(api: ServerRequester, libraryId: string, signal?: AbortSignal): Promise<LibraryTrash> {
  const [library, missing]: [unknown, unknown] = await Promise.all([
    callServer(api, 'GET /v1/admin/libraries/{id}', {id: libraryId, signal}),
    callServer(api, 'GET /v1/admin/libraries/{id}/missing', {id: libraryId, signal}),
  ]);
  if (!object(library) || typeof library.name !== 'string' || !object(library.options) || !object(missing) || !Array.isArray(missing.items) || typeof missing.total !== 'number') throw invalid();
  const items = missing.items.map((m: unknown): MissingTitle => {
    if (!object(m) || typeof m.id !== 'string' || typeof m.title !== 'string' || !time(m.missingSinceAt) || m.removedAt !== undefined && !time(m.removedAt)) throw invalid();
    return Object.freeze({id: m.id, title: m.title, ...(typeof m.year === 'number' && m.year > 0 ? {year: m.year} : {}), missingSince: m.missingSinceAt, ...(m.removedAt ? {removedAt: m.removedAt as string} : {})});
  });
  return Object.freeze({name: library.name, rule: ruleOf(library.options), options: library.options as unknown as LibraryOptions, items: Object.freeze(items), total: missing.total});
}

/** Saves the library's rule. The server replaces a library's options whole, so the others go back as they were read. */
export async function saveTrashRule(api: ServerRequester, libraryId: string, options: LibraryOptions, rule: TrashRule, signal?: AbortSignal): Promise<TrashRule> {
  const trash = rule.mode === 'days' ? {mode: 'days', days: rule.days} : {mode: rule.mode};
  const saved: AdminLibrary = await callServer(api, 'PATCH /v1/admin/libraries/{id}', {id: libraryId, body: {options: {...options, trash}}, signal});
  return ruleOf(saved?.options);
}

/** Empties the trash now. `typed` is the count the owner typed; the server checks it against what is missing. */
export async function emptyLibraryTrash(api: ServerRequester, libraryId: string, typed: string, signal?: AbortSignal): Promise<number> {
  const answer: unknown = await callServer(api, 'DELETE /v1/admin/libraries/{id}/missing', {id: libraryId, query: {confirm: typed}, signal});
  return object(answer) && typeof answer.removed === 'number' ? answer.removed : 0;
}
