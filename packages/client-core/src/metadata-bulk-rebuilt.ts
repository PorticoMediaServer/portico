/**
 * Bulk metadata edit on the rebuilt server (definition 04 rules 27 to 30).
 *
 * The preview reads each chosen title's document (`GET /v1/admin/items/{id}/metadata`) and works
 * out here what the titles share and what is Mixed. The fields offered are those the rebuilt
 * server edits in bulk (`edit_items`) and every chosen title has. Saving is one `edit_items` job
 * (`POST /v1/jobs`): each title is its own change, a list gains and loses entries and is never
 * replaced, and asking for the same edit again saves the titles that were not saved and changes
 * nothing on the rest. When the job ends without saving every title, each title is read again,
 * so the result still says title by title which were saved.
 */
import type {Job, JobStart, MetadataDocument} from '../../contracts/src/next/api.ts';
import type {BulkMetadataField, BulkMetadataPreview, BulkMetadataPreviewTarget, BulkMetadataValueCount} from './home-selection/metadata-preview.ts';
import type {BulkEdit, BulkListEdit} from './metadata-bulk.ts';
import type {RepairFieldSpec} from './metadata-repair.ts';
import {serverPath} from './query/server-api.ts';

export interface BulkMetadataApi {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}

/** The rebuilt server keeps no viewer fence; one constant fence means a later preview is never "another viewer's". */
const fence = '0'.repeat(64);
/** At most this many titles in one edit (`edit_items`). */
export const bulkEditLimit = 1000;
/** The values and list entries shown for one field, as before. */
const shown = 64;

/** What `edit_items` changes, in the editor's words (labels are Tags). */
const scalarFields: readonly RepairFieldSpec[] = [
  {field: 'contentRating', label: 'Content rating', group: 'general', type: 'text', bulk: true, maxLength: 64},
  {field: 'year', label: 'Year', group: 'numbers', type: 'integer', bulk: true, min: 0, max: 9999},
  {field: 'edition', label: 'Edition', group: 'general', type: 'text', bulk: true, maxLength: 128},
];
const listFields: readonly RepairFieldSpec[] = [
  {field: 'genres', label: 'Genres', group: 'general', type: 'list', bulk: true},
  {field: 'studios', label: 'Studio', group: 'general', type: 'list', bulk: true},
  {field: 'networks', label: 'Network', group: 'general', type: 'list', bulk: true},
  {field: 'countries', label: 'Country', group: 'general', type: 'list', bulk: true},
  {field: 'tags', label: 'Tags', group: 'general', type: 'list', bulk: true},
];
const oneAtTime: readonly RepairFieldSpec[] = Object.entries({title: 'Title', sortTitle: 'Sort title', originalTitle: 'Original title', tagline: 'Tagline', summary: 'Description', releaseDate: 'Release date'})
  .map(([field, label]) => ({field, label, group: 'general', type: 'text', bulk: false}));
const listField = (field: string) => field === 'tags' ? 'labels' : field;
const fail = (code: string) => Object.assign(new Error(code), {code});
const asText = (value: unknown): string => value === undefined || value === null ? '' : String(value);

/** Reads every chosen title's document, a few at a time. */
async function documents(api: BulkMetadataApi, ids: readonly string[], signal?: AbortSignal, isCurrent?: () => boolean): Promise<MetadataDocument[]> {
  const out: MetadataDocument[] = new Array(ids.length);
  let next = 0;
  const worker = async () => {
    for (let at = next++; at < ids.length; at = next++) {
      if (signal?.aborted || isCurrent && !isCurrent()) throw fail('cancelled');
      out[at] = await api.request<MetadataDocument>(serverPath('GET /v1/admin/items/{id}/metadata', {id: ids[at]!}), 'GET', undefined, signal);
    }
  };
  await Promise.all(Array.from({length: Math.min(6, ids.length)}, worker));
  return out;
}

function counted(values: readonly string[], titles?: readonly string[]): {rows: BulkMetadataValueCount[]; distinct: number} {
  const counts = new Map<string, {count: number; titles: string[]}>();
  values.forEach((value, index) => {
    const row = counts.get(value) ?? {count: 0, titles: []};
    row.count++;
    if (titles && row.titles.length < 5 && titles[index]) row.titles.push(titles[index]!);
    counts.set(value, row);
  });
  const rows = [...counts].sort((a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0])).slice(0, shown)
    .map(([value, row]) => Object.freeze({value, count: row.count, ...(titles && row.titles.length ? {titles: Object.freeze(row.titles)} : {})}));
  return {rows, distinct: counts.size};
}

/** What the titles share, what is Mixed, and how many have the field locked. */
function aggregate(values: readonly string[], titles: readonly string[], entries: readonly string[][] | undefined, locked: number): BulkMetadataField {
  const {rows, distinct} = counted(values, titles);
  const entryRows = entries ? counted(entries.flat()) : {rows: [], distinct: 0};
  const truncated = distinct > shown || entryRows.distinct > shown;
  if (distinct > shown) return Object.freeze({state: 'many', atLeast: shown + 1, values: Object.freeze([]), entries: Object.freeze(entryRows.rows), locked, truncated: true});
  return Object.freeze({state: distinct === 1 ? 'shared' : 'mixed', distinctCount: distinct, ...(distinct === 1 ? {value: rows[0]!.value} : {}),
    values: Object.freeze(rows), entries: Object.freeze(entryRows.rows), locked, truncated});
}

/** The bulk editor's preview of the chosen titles. */
export async function readBulkMetadataPreview(api: BulkMetadataApi, targets: readonly BulkMetadataPreviewTarget[], serverId: string,
  signal?: AbortSignal, isCurrent?: () => boolean): Promise<BulkMetadataPreview> {
  if (!targets.length || targets.length > bulkEditLimit) throw fail('metadata-preview-limit');
  const docs = await documents(api, targets.map(target => target.id), signal, isCurrent);
  // Titles edited together are one kind (rule 27).
  if (docs.some((doc, index) => !doc || doc.itemId !== targets[index]!.id || doc.kind !== docs[0]!.kind)) throw fail('metadata-one-kind-required');
  const titles = docs.map(doc => asText(doc.fields?.title?.value));
  const schema = [...scalarFields.filter(spec => docs.every(doc => doc.fields && spec.field in doc.fields)),
    ...listFields.filter(spec => docs.every(doc => doc.lists && listField(spec.field) in doc.lists))];
  const fields: Record<string, BulkMetadataField> = {};
  for (const spec of schema) {
    if (spec.type === 'list') {
      const lists = docs.map(doc => [...(doc.lists[listField(spec.field)]?.values ?? [])]);
      fields[spec.field] = aggregate(lists.map(list => [...list].sort().join(', ')), titles, lists, docs.filter(doc => doc.lists[listField(spec.field)]?.locked).length);
    } else fields[spec.field] = aggregate(docs.map(doc => asText(doc.fields[spec.field]?.value)), titles, undefined, docs.filter(doc => doc.fields[spec.field]?.locked).length);
  }
  const excluded = oneAtTime.filter(spec => docs.every(doc => doc.fields && spec.field in doc.fields));
  const first = targets[0]!;
  return Object.freeze({serverId, viewerFence: fence, contentKind: first.contentKind, kind: first.kind, total: targets.length,
    schema: Object.freeze(schema), singleItemFields: Object.freeze([...schema, ...excluded]), excludedFields: Object.freeze(excluded), fields: Object.freeze(fields),
    targets: Object.freeze(targets.map((target, index) => Object.freeze({kind: target.kind, id: target.id, libraryId: target.libraryId, expectedRevision: docs[index]!.revision})))});
}

const listNames: Readonly<Record<string, string>> = {genres: 'genres', studios: 'studios', networks: 'networks', countries: 'countries', labels: 'labels', tags: 'labels'};
/** The editor's edit as `edit_items` parameters. */
function bulkParams(edit: BulkEdit): Record<string, unknown> {
  const params: Record<string, unknown> = {}, add: Record<string, string[]> = {}, remove: Record<string, string[]> = {};
  for (const [field, change] of Object.entries(edit.fields ?? {})) {
    if (change.value === undefined || change.values !== undefined || change.useAutomatic) throw fail('metadata-edit-unavailable');
    if (field === 'year') {
      const year = Number(change.value.trim());
      if (!Number.isInteger(year) || year < 0 || year > 9999) throw fail('metadata-edit-unavailable');
      params.year = year;
    } else if (field === 'edition' || field === 'contentRating') params[field] = change.value.trim();
    else throw fail('metadata-edit-unavailable');
  }
  const lists: [string, BulkListEdit][] = [...Object.entries(edit.lists ?? {}), ...(edit.genres ? [['genres', edit.genres] as [string, BulkListEdit]] : [])];
  for (const [name, list] of lists) {
    const wire = listNames[name];
    if (!wire) throw fail('metadata-edit-unavailable');
    if (list.add?.length) add[wire] = list.add.map(value => value.trim());
    if (list.remove?.length) remove[wire] = list.remove.map(value => value.trim());
  }
  return {...params, ...(Object.keys(add).length ? {add} : {}), ...(Object.keys(remove).length ? {remove} : {}), lock: edit.lockEdited !== false};
}

/** Whether a title's document now shows the edit. */
function applied(doc: MetadataDocument, params: Record<string, unknown>): boolean {
  for (const field of ['year', 'edition', 'contentRating']) if (field in params
    && (asText(doc.fields?.[field]?.value) !== asText(params[field]) || params.lock === true && !doc.fields?.[field]?.locked)) return false;
  const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();
  for (const [name, values] of Object.entries((params.add ?? {}) as Record<string, string[]>)) if (!values.every(value => doc.lists?.[name]?.values.some(entry => same(entry, value)))) return false;
  for (const [name, values] of Object.entries((params.remove ?? {}) as Record<string, string[]>)) if (values.some(value => doc.lists?.[name]?.values.some(entry => same(entry, value)))) return false;
  if (params.lock === true) for (const name of new Set([...Object.keys(params.add ?? {}), ...Object.keys(params.remove ?? {})])) {
    if (name !== 'labels' && !doc.lists?.[name]?.locked) return false;
  }
  return true;
}

const sleep = (ms: number, signal?: AbortSignal) => new Promise<void>((resolve, reject) => {
  const timer = setTimeout(() => {signal?.removeEventListener('abort', stop); resolve();}, ms);
  const stop = () => {clearTimeout(timer); reject(fail('cancelled'));};
  signal?.addEventListener('abort', stop, {once: true});
});

/**
 * Saves one bulk edit as an `edit_items` job and answers the editor's receipt: one row per title, saved or not and
 * why. Sent again (Try again), the same edit saves what was not saved and changes nothing on the rest.
 */
export async function saveBulkMetadata(api: BulkMetadataApi, edit: BulkEdit, signal?: AbortSignal): Promise<unknown> {
  if (!edit.targets.length || edit.targets.length > bulkEditLimit) throw fail('metadata-edit-limit');
  const params = bulkParams(edit), start: JobStart = {kind: 'edit_items', target: {items: edit.targets.map(target => target.id)}, params};
  let job = await api.request<Job>(serverPath('POST /v1/jobs'), 'POST', start, signal);
  for (const started = Date.now(); job.state === 'waiting' || job.state === 'running';) {
    if (Date.now() - started > 300_000) throw Object.assign(fail('job_poll_timeout'), {retryable: true});
    await sleep(1000, signal);
    job = await api.request<Job>(serverPath('GET /v1/jobs/{id}', {id: job.id}), 'GET', undefined, signal);
  }
  let results;
  if (job.state === 'done') results = edit.targets.map(target => ({kind: target.kind, id: target.id, ok: true, revision: target.expectedRevision}));
  else {
    // The job names what it could not save in one sentence; each title is read again so the result is title by title.
    const message = job.error || 'This title could not be saved.';
    const docs = await documents(api, edit.targets.map(target => target.id), signal).catch(() => undefined);
    results = edit.targets.map((target, index) => docs?.[index] && applied(docs[index]!, params)
      ? {kind: target.kind, id: target.id, ok: true, revision: docs[index]!.revision}
      : {kind: target.kind, id: target.id, ok: false, code: 'metadata_not_saved', message});
  }
  return {operationId: edit.operationId, results, updated: results.filter(row => row.ok).length, failed: results.filter(row => !row.ok).length};
}
