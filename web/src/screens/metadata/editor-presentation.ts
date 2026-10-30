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
