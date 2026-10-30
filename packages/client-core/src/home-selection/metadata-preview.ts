import type {HomeApi} from '../home.ts';
import type {JobSelector} from '../bulk-jobs.ts';
import type {RepairFieldSpec} from '../metadata-repair.ts';
import type {SelectionIdentity} from '../selection-model/index.ts';
import {selectionExecutionError} from './execution.ts';
import {readMetadataPreview, type MetadataPreviewLifetime} from './metadata-preview-preparation.ts';

export type BulkMetadataPreviewTarget = Readonly<{kind: 'item' | 'show' | 'season' | 'album' | 'artist' | 'book'; contentKind: string; id: string; libraryId: string}>;
export type BulkMetadataPreviewRequest = Readonly<{targets: readonly BulkMetadataPreviewTarget[]; selector?: never; expected?: never} | {targets?: never; selector: JobSelector; expected: {catalogRevision: string}}>;
export type BulkMetadataValueCount = Readonly<{value: string; count: number}>;
/** Exact small cardinalities and a lower bound are deliberately different states. */
export type BulkMetadataField = Readonly<{
  values: readonly BulkMetadataValueCount[]; entries: readonly BulkMetadataValueCount[]; locked: number; truncated: boolean;
} & ({state: 'shared' | 'mixed'; distinctCount: number; atLeast?: never; value?: string}
  | {state: 'many'; distinctCount?: never; atLeast: number; value?: never})>;
export type BulkMetadataPreview = Readonly<{
  serverId: string; viewerFence: string; contentKind: string; kind: BulkMetadataPreviewTarget['kind']; total: number; selectionRevision?: string;
  tabs?: readonly string[];schema: readonly RepairFieldSpec[];
  singleItemFields: readonly RepairFieldSpec[];
  excludedFields: readonly RepairFieldSpec[];
  fields: Readonly<Record<string, BulkMetadataField>>;
  targets: readonly Readonly<{kind: BulkMetadataPreviewTarget['kind']; id: string; libraryId: string; expectedRevision: string}>[];
}>;
export function metadataPreviewTargets(identities: readonly SelectionIdentity[]): readonly BulkMetadataPreviewTarget[] {
  return identities.map(identity => {
    if (identity.kind === 'channel' || identity.kind === 'person') throw selectionExecutionError('metadata-not-offered');
    const kind = ['show', 'season', 'album', 'artist', 'book'].includes(identity.kind) ? identity.kind as Exclude<BulkMetadataPreviewTarget['kind'], 'item'> : 'item';
    return {kind, contentKind: identity.kind, id: identity.id, libraryId: identity.libraryId};
  });
}
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const text = (value: unknown): value is string => typeof value === 'string' && value.length <= 65536 && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(value);
const id = (value: unknown): value is string => text(value) && !!value && value.length <= 256 && !/[\r\n\t]/.test(value);
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const hash = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
function bad(): never {throw selectionExecutionError('invalid-metadata-preview');}
export function parseBulkMetadataPreview(raw: unknown, request: BulkMetadataPreviewRequest, serverId: string): BulkMetadataPreview {
  if (!record(raw) || raw.serverId !== serverId || !hash(raw.viewerFence) || !id(raw.contentKind) || !['item', 'show', 'season', 'album', 'artist', 'book'].includes(String(raw.kind)) || !count(raw.total) || raw.total < 1 || !Array.isArray(raw.schema) || raw.schema.length > 32 || !record(raw.fields) || !Array.isArray(raw.targets)) bad();
  const parseSchema = (input: unknown, bulkOnly = false): readonly RepairFieldSpec[] => {
    if (!Array.isArray(input) || input.length > 32) bad();
    const parsed = input.map(value => {
    if (!record(value) || !id(value.field) || !text(value.label) || !['general', 'numbers', 'text', 'identity'].includes(String(value.group)) || !['text', 'multiline', 'integer', 'number', 'date', 'enum', 'list'].includes(String(value.type)) || typeof value.bulk !== 'boolean' || bulkOnly && !value.bulk) bad();
    if (value.maxLength !== undefined && !count(value.maxLength) || value.min !== undefined && !count(value.min) || value.max !== undefined && !count(value.max) || value.allowed !== undefined && (!Array.isArray(value.allowed) || value.allowed.length > 200 || !value.allowed.every(text))) bad();
    return Object.freeze({field: value.field, label: value.label as string, group: value.group as RepairFieldSpec['group'], type: value.type as RepairFieldSpec['type'], bulk: value.bulk, ...(value.maxLength !== undefined ? {maxLength: value.maxLength as number} : {}), ...(value.min !== undefined ? {min: value.min as number} : {}), ...(value.max !== undefined ? {max: value.max as number} : {}), ...(value.allowed !== undefined ? {allowed: Object.freeze([...(value.allowed as string[])])} : {})});
    });
    if (new Set(parsed.map(field => field.field)).size !== parsed.length) bad(); return Object.freeze(parsed);
  };
  const schema = parseSchema(raw.schema, true), singleItemFields = parseSchema(raw.singleItemFields), excludedFields = parseSchema(raw.excludedFields);
  if (JSON.stringify(excludedFields) !== JSON.stringify(singleItemFields.filter(field => !field.bulk))) bad();
  if (singleItemFields.filter(field => field.bulk).some(field => !schema.some(candidate => JSON.stringify(candidate) === JSON.stringify(field)))) bad();
  if (new Set(schema.map(field => field.field)).size !== schema.length || Object.keys(raw.fields).length !== schema.length) bad();
  const fields: Record<string, BulkMetadataField> = Object.create(null);
  const values = (value: unknown): readonly BulkMetadataValueCount[] => {
    if (!Array.isArray(value) || value.length > 64) bad();
    const rows = value.map(row => {if (!record(row) || !text(row.value) || !count(row.count) || row.count < 1 || row.count > (raw.total as number)) bad(); return Object.freeze({value: row.value, count: row.count});});
    if (new Set(rows.map(row => row.value)).size !== rows.length) bad(); return Object.freeze(rows);
  };
  for (const spec of schema) {
    const field = raw.fields[spec.field];
    if (!record(field) || !['shared', 'mixed', 'many'].includes(String(field.state)) || !count(field.locked) || field.locked > raw.total || typeof field.truncated !== 'boolean') bad();
    if (field.state === 'shared' ? !text(field.value) : field.value !== undefined) bad();
    const variants = values(field.values), entries = values(field.entries);
    if (field.state === 'many') {
      // The bounded reducer observes exactly K+1 keys; this is never an exact count.
      if (field.distinctCount !== undefined || field.atLeast !== 65 || field.atLeast > raw.total || variants.length || !field.truncated) bad();
      fields[spec.field] = Object.freeze({state: 'many', atLeast: field.atLeast, values: variants, entries, locked: field.locked, truncated: true});
      continue;
    }
    if (field.atLeast !== undefined || !count(field.distinctCount) || field.distinctCount < 1 || field.distinctCount > 64 || field.distinctCount > raw.total || !variants.length) bad();
    if (field.state === 'shared' ? field.distinctCount !== 1 : field.distinctCount < 2) bad();
    if (field.distinctCount !== variants.length) bad();
    if (field.state === 'shared' && (variants.length !== 1 || variants[0].value !== field.value || variants[0].count !== raw.total) || variants.reduce((n, row) => n + row.count, 0) !== raw.total) bad();
    fields[spec.field] = Object.freeze({state: field.state as 'shared' | 'mixed', distinctCount: field.distinctCount, ...(field.state === 'shared' ? {value: field.value as string} : {}), values: variants, entries, locked: field.locked, truncated: field.truncated});
  }
  const requested = request.targets;
  if (requested ? raw.targets.length !== requested.length || raw.total !== requested.length : raw.targets.length !== 0 || raw.selectionRevision !== request.expected.catalogRevision) bad();
  const targets = raw.targets.map((row, index) => {
    const expected = requested?.[index];
    if (!record(row) || !expected || row.kind !== expected.kind || row.id !== expected.id || row.libraryId !== expected.libraryId || !hash(row.expectedRevision) || raw.contentKind !== expected.contentKind) bad();
    return Object.freeze({kind: expected.kind, id: expected.id, libraryId: expected.libraryId, expectedRevision: row.expectedRevision});
  });
  return Object.freeze({serverId, viewerFence: raw.viewerFence, contentKind: raw.contentKind, kind: raw.kind as BulkMetadataPreviewTarget['kind'], total: raw.total, ...(typeof raw.selectionRevision === 'string' ? {selectionRevision: raw.selectionRevision} : {}), ...(Array.isArray(raw.tabs)?{tabs:Object.freeze(raw.tabs.filter((tab):tab is string=>typeof tab==='string'&&['general','numbers','text','identity'].includes(tab)))}:{}),schema, singleItemFields, excludedFields, fields: Object.freeze(fields), targets: Object.freeze(targets)});
}
export async function fetchBulkMetadataPreview(api: HomeApi, request: BulkMetadataPreviewRequest, serverId: string, signal?: AbortSignal, lifetime: Omit<MetadataPreviewLifetime, 'signal'> = {}): Promise<BulkMetadataPreview> {
  if (request.targets && (!request.targets.length || request.targets.length > 2048)) throw selectionExecutionError('metadata-preview-limit');
  return readMetadataPreview(api, request, (raw, captured) => parseBulkMetadataPreview(raw, captured, serverId), {...lifetime, signal});
}
