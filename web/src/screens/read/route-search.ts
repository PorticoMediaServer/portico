import type {WebScreenSearch} from '../../route-registry';

/** Opaque identity published by ReadingClient.list/versions, never a URL. */
export function readSearch(raw: WebScreenSearch): {sourceId?: string} {
  return typeof raw.sourceId === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(raw.sourceId) ? {sourceId: raw.sourceId} : {};
}

export type ReadTarget = Readonly<{itemId: string; sourceId?: string}>;
export function retainedReadTarget(previous: ReadTarget | undefined, active: ReadTarget | undefined, settings: boolean): ReadTarget | undefined {
  if (!active) return settings ? previous : undefined;
  return previous?.itemId === active.itemId && previous.sourceId === active.sourceId ? previous : Object.freeze({...active});
}
