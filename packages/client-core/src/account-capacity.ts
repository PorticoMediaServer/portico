/** A scoped invalidation only. It contains no capacity value and grants no
 * permission to start; callers still refresh own-account controls and admit the
 * exact current captured request normally. */
export const capacityChangedEventType = 'capacity.changed' as const;
export type CapacityChangedEvent = Readonly<{variant: 'capacity'; id: string; type: typeof capacityChangedEventType; at: string}>;

/** Read the closed raw envelope before any generic event projection can discard
 * unexpected fields. Normal opaque sequencing/time are the only metadata. */
export function parseCapacityChangedEvent(value: unknown): CapacityChangedEvent | undefined {
 if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
 const raw = value as Record<string, unknown>;
 if (Object.keys(raw).length !== 4 || Object.keys(raw).some(key => key !== 'variant' && key !== 'id' && key !== 'type' && key !== 'at') || raw.variant !== 'capacity'
  || raw.type !== capacityChangedEventType || typeof raw.id !== 'string' || raw.id.length < 1 || raw.id.length > 128
  || /[\u0000-\u001f\u007f]/.test(raw.id) || typeof raw.at !== 'string'
  || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(raw.at) || !Number.isFinite(Date.parse(raw.at))) return undefined;
 return Object.freeze({variant: 'capacity' as const, id: raw.id, type: capacityChangedEventType, at: raw.at});
}
