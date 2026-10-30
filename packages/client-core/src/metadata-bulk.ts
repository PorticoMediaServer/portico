import {unreadableServerResponse} from './server-messages.ts';
/**
 * A bulk metadata edit is many independent single-entity edits behind one
 * request. Each target carries its own fence and reports its own outcome, so a
 * partial failure is a normal result to render, never an error to retry blindly.
 */
import type {RepairFieldSpec} from './metadata-repair.ts';

export type BulkTarget = Readonly<{kind: 'item' | 'show' | 'season' | 'album' | 'artist' | 'book'; id: string; expectedRevision: string}>;
export type BulkListEdit = Readonly<{add?: readonly string[]; remove?: readonly string[]}>;
export type BulkFieldEdit = Readonly<{value?: string; values?: readonly string[]; locked?: boolean; useAutomatic?: boolean}>;
export type BulkEdit = Readonly<{
  operationId: string;
  targets: readonly BulkTarget[];
  fields?: Readonly<Record<string, BulkFieldEdit>>;
  lists?: Readonly<{tags?: BulkListEdit; labels?: BulkListEdit}>;
  genres?: BulkListEdit;
  lockEdited?: boolean;
}>;
export type BulkResult = Readonly<{kind: string; id: string; ok: boolean; code?: string; message?: string; revision?: string}>;
export type BulkReceipt = Readonly<{operationId: string; results: readonly BulkResult[]; updated: number; failed: number}>;

export const bulkTargetLimit = 200;

const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const str = (v: unknown, max: number): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const hash = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const kinds = ['item', 'show', 'season', 'album', 'artist', 'book'];

function bad(): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_metadata_repair'});
}

/** A list entry the server will accept: trimmed, bounded, no control characters. */
export function validBulkListEntry(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0 && [...value].length <= 128 && !/[\x00-\x1f\x7f-\x9f]/.test(value);
}

/**
 * Refuse a bulk edit the server would refuse, before it is sent: the registry
 * decides which fields may be edited in bulk, and only the schema shared by
 * every selected target can be offered.
 */
export function validBulkEdit(edit: BulkEdit, schema: readonly RepairFieldSpec[]): boolean {
  if (!obj(edit) || !str(edit.operationId, 160) || !/^[A-Za-z0-9_.-]+$/.test(edit.operationId)) return false;
  if (!Array.isArray(edit.targets) || edit.targets.length === 0 || edit.targets.length > bulkTargetLimit) return false;
  const kind = edit.targets[0].kind;
  const seen = new Set<string>();
  for (const target of edit.targets) {
    if (target.kind !== kind || !kinds.includes(target.kind) || !str(target.id, 256) || !target.id || !hash(target.expectedRevision)) return false;
    if (seen.has(target.id)) return false;
    seen.add(target.id);
  }
  const fields = edit.fields ?? {};
  const bulkable = new Map(schema.filter(s => s.bulk).map(s => [s.field, s]));
  for (const [field, value] of Object.entries(fields)) {
    const spec = bulkable.get(field);
    if (!spec || !obj(value)) return false;
    const chosen = [value.value !== undefined, value.values !== undefined, value.useAutomatic === true].filter(Boolean).length;
    if (chosen === 0 && value.locked === undefined) return false;
    if (chosen > 1) return false;
    if (value.values !== undefined && (spec.type !== 'list' || !Array.isArray(value.values) || value.values.length > 64 || !value.values.every(validBulkListEntry))) return false;
    if (value.value !== undefined && !str(value.value, 65536)) return false;
  }
  for (const [name, list] of Object.entries(edit.lists ?? {})) {
    const spec = bulkable.get(name);
    if (!spec || spec.type !== 'list' || !obj(list)) return false;
    if (!validList(list)) return false;
  }
  if (edit.genres !== undefined && (kind !== 'item' || !validList(edit.genres))) return false;
  return Object.keys(fields).length > 0 || Object.keys(edit.lists ?? {}).length > 0 || edit.genres !== undefined;
}

function validList(list: unknown): boolean {
  if (!obj(list)) return false;
  for (const key of Object.keys(list)) if (key !== 'add' && key !== 'remove') return false;
  for (const side of [list.add, list.remove]) {
    if (side === undefined) continue;
    if (!Array.isArray(side) || side.length > 64 || !side.every(validBulkListEntry)) return false;
  }
  return list.add !== undefined || list.remove !== undefined;
}

/** Validate the receipt, and check it answers exactly the targets that were sent. */
export function validateBulkReceipt(raw: unknown, edit: BulkEdit): BulkReceipt {
  if (!obj(raw) || raw.operationId !== edit.operationId || !count(raw.updated) || !count(raw.failed)) bad();
  if (!Array.isArray(raw.results) || raw.results.length !== edit.targets.length) bad();
  const results = raw.results.map((row, index) => {
    const target = edit.targets[index];
    if (!obj(row) || row.kind !== target.kind || row.id !== target.id || typeof row.ok !== 'boolean') bad();
    if (row.ok && !hash(row.revision)) bad();
    if (!row.ok && (!str(row.code, 64) || !row.code || !str(row.message, 2048))) bad();
    // A receipt says what happened to each change that was sent, so every row stays exact: a row
    // that cannot be read would leave an outcome unknown. Only what a success carries beyond its
    // revision (a field this app does not use) is ignored.
    return Object.freeze({
      kind: row.kind as string, id: row.id as string, ok: row.ok as boolean,
      ...(row.ok ? {revision: row.revision as string} : {code: row.code as string, message: row.message as string}),
    });
  });
  if (results.filter(r => r.ok).length !== raw.updated || results.filter(r => !r.ok).length !== raw.failed) bad();
  return Object.freeze({operationId: raw.operationId as string, results: Object.freeze(results), updated: raw.updated, failed: raw.failed});
}

/** The targets a caller should retry after reloading: only the stale ones. */
export function bulkConflicts(receipt: BulkReceipt): readonly string[] {
  return Object.freeze(receipt.results.filter(r => !r.ok && r.code === 'metadata_conflict').map(r => r.id));
}
