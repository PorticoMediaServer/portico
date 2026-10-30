export function metadataText(value: unknown): string | undefined {
  if (typeof value === 'string') return value || undefined;
  if (Array.isArray(value)) return value.map(metadataText).filter(Boolean).join(', ') || undefined;
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return metadataText(record.name) ?? metadataText(Object.values(record).find(item => typeof item === 'string'));
  }
}
/** dc:language is repeatable; a document lang attribute accepts one tag. */
export function metadataLanguage(value: unknown): string | undefined {
  if (Array.isArray(value)) {
    for (const item of value) { const language = metadataLanguage(item); if (language) return language; }
    return undefined;
  }
  return metadataText(value);
}
