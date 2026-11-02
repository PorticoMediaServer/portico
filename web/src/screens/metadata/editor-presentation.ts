import type {RepairData, RepairFieldSpec} from '@core/metadata-repair.ts';
import type {MetadataFieldDraft} from './draft-value';

export type MetadataTab = 'general' | 'artwork' | 'tags' | 'people' | 'matching' | 'lyrics' | 'history';

/** Counts describe the records in this editor response (and the visible tag
 * draft), never an inferred catalogue total. */
export function metadataTabCount(tab: MetadataTab, data: RepairData | null | undefined, draft: Readonly<Record<string, MetadataFieldDraft>>): number | undefined {
  if (!data) return undefined;
  switch (tab) {
    case 'artwork': return data.artwork.candidates.length;
    case 'tags': return data.schema.filter(field => field.type === 'list').reduce((count, field) => count + (draft[field.field]?.values ?? data.snapshot.fields[field.field]?.values ?? []).length, 0);
    case 'people': return data.snapshot.relationships.filter(row => row.kind === 'credit').length;
    case 'matching': return data.candidates.length;
    case 'history': return data.history.length;
    default: return undefined;
  }
}

/** Only the drawn short fields share a row on compact windows. The schema
 * still decides which fields exist and the order in which they are rendered. */
export function compactMetadataField(spec: Pick<RepairFieldSpec, 'field' | 'type'>): boolean {
  return ['integer', 'number', 'date'].includes(spec.type) || ['contentRating', 'rating', 'edition', 'network', 'recordLabel', 'series'].includes(spec.field);
}

/** owner-metadata frame 6: where a value came from, said the way an owner says it.
 * `automatic` is an empty value with no origin; the field's own hint shows instead. */
export type MetadataFieldSource = Readonly<{kind: 'provider'; provider: string} | {kind: 'edited'} | {kind: 'library'}>;
export function metadataFieldSource(source: string | undefined): MetadataFieldSource | undefined {
  if (!source || source === 'automatic') return undefined;
  if (source === 'manual') return {kind: 'edited'};
  if (source === 'scanner') return {kind: 'library'};
  const provider = source.startsWith('provider:') ? source.slice('provider:'.length) : source;
  return provider ? {kind: 'provider', provider} : undefined;
}

/** Frame 6 counts only where length matters (the description); a number gives its range. */
export function metadataFieldMeter(spec: Pick<RepairFieldSpec, 'type' | 'min' | 'max' | 'maxLength'>): 'range' | 'count' | undefined {
  if (spec.type === 'integer' || spec.type === 'number') return spec.min !== undefined || spec.max !== undefined ? 'range' : undefined;
  return spec.type === 'multiline' && spec.maxLength !== undefined ? 'count' : undefined;
}

/** A long value spans both columns. */
export function wideMetadataField(spec: Pick<RepairFieldSpec, 'field' | 'type'>): boolean {
  return spec.type === 'multiline' || spec.field === 'tagline';
}

const GENERAL_ORDER = ['title', 'sortTitle', 'originalTitle', 'metadataLanguage', 'year', 'releaseDate', 'contentRating', 'edition', 'studio', 'network', 'country'];
/** Frame 6 order: names first, then dates and ratings, then companies; long text last.
 * Fields the frame does not draw keep the server's order, after the drawn ones. */
export function generalMetadataFields<T extends Pick<RepairFieldSpec, 'field' | 'type' | 'group'>>(schema: readonly T[]): readonly T[] {
  const rank = (spec: T) => wideMetadataField(spec) ? GENERAL_ORDER.length + 1 + (spec.type === 'multiline' ? 1 : 0)
    : GENERAL_ORDER.includes(spec.field) ? GENERAL_ORDER.indexOf(spec.field) : GENERAL_ORDER.length;
  return schema.filter(spec => spec.type !== 'list' && spec.group !== 'identity')
    .map((spec, index) => ({spec, index})).sort((a, b) => rank(a.spec) - rank(b.spec) || a.index - b.index).map(row => row.spec);
}
