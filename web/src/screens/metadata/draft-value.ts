import type {RepairData} from '@core/metadata-repair.ts';

/** Lock-only edits must retain a list's values, just as the visible Tags field does. */
export function metadataDraftValue(data: RepairData | null | undefined, field: string): string {
  const original = data?.snapshot.fields[field];
  return data?.schema.find(spec => spec.field === field)?.type === 'list'
    ? (original?.values ?? []).join(', ')
    : original?.value ?? '';
}

export type MetadataFieldDraft = Readonly<{value: string; locked: boolean; values?: readonly string[]}>;
/** Editing protects the new value; a deliberate lock toggle keeps the existing draft value. */
export function patchMetadataDraft(data: RepairData | null | undefined, field: string, current: MetadataFieldDraft | undefined, patch: Partial<MetadataFieldDraft>): MetadataFieldDraft {
  const list = data?.schema.find(spec => spec.field === field)?.type === 'list';
  const values = list ? patch.values ?? current?.values ?? data?.snapshot.fields[field]?.values ?? [] : undefined;
  return {value: values ? values.join(', ') : patch.value ?? current?.value ?? metadataDraftValue(data, field),
    ...(values ? {values: [...values]} : {}),
    locked: patch.locked ?? (patch.value !== undefined || patch.values !== undefined ? true : current?.locked ?? data?.snapshot.fields[field]?.locked ?? false)};
}
