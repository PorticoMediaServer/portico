/** Errors are machine codes; renderers present catalogue messages at their boundary. */
export function invalidState(): never { throw new Error('invalid_player_state'); }
export const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
export const wireId = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
/** v1 queue occurrences also contain dots; other wire identity domains stay strict. */
export const queueOccurrenceId = (v: unknown): v is string => typeof v === 'string' && v.length > 0 && v.length <= 128 && !/[^A-Za-z0-9_.-]/.test(v);
export const counter = (v: unknown): v is string => typeof v === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(v) && BigInt(v) <= 9223372036854775807n;
export const seconds = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 1e9;
export function clockMs(v: number): number { if (!Number.isSafeInteger(v) || v < 0) invalidState(); return v; }
