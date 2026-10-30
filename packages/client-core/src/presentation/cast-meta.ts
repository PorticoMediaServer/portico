import {apiRoot} from "../../../contracts/src/routes.ts";
/**
 * MU4 CAST-03: the cast title's display facts, remembered where the cast
 * started. The receiver protocol carries no artwork and the sender's snapshot
 * keeps no meta, so the expanded controller and the collapsed thumbnail read
 * it here instead of issuing a request per open.
 */
export type CastMeta = Readonly<{title: string; subtitle?: string; artworkPath?: string}>;

const metas = new Map<string, CastMeta>();
const MAX = 20;

/** Remember what was sent to the TV (bounded: the last few titles). */
export function noteCastMeta(itemId: string, meta: CastMeta): void {
  if (typeof itemId !== 'string' || !itemId || !meta || typeof meta.title !== 'string') return;
  metas.set(itemId, Object.freeze({title: meta.title.slice(0, 200), ...(meta.subtitle ? {subtitle: meta.subtitle.slice(0, 200)} : {}), ...(meta.artworkPath && meta.artworkPath.startsWith(apiRoot) ? {artworkPath: meta.artworkPath.slice(0, 512)} : {})}));
  while (metas.size > MAX) metas.delete(metas.keys().next().value!);
}

/** What was sent for `itemId`, if this device sent it. */
export function castMeta(itemId: string | undefined): CastMeta | undefined {
  return typeof itemId === 'string' ? metas.get(itemId) : undefined;
}
