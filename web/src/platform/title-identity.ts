import {encodedRoutePath} from '../../../packages/contracts/src/routes.ts';

/** A title's own library and kind, read from its detail route. Unlike the bare item read
 * (`GET /v1/items/{id}`, timed media only), the detail route serves every kind the viewer may
 * see, Reading included, under the same privacy fence, so a Reading title never "vanishes"
 * behind the wrong route (P2-74 E-web-23). The answer must name exactly this title. */
export type TitleIdentity = Readonly<{id: string; libraryId: string; kind: string; title: string; posterUrl?: string; backdropUrl?: string}>;
type Requester = Readonly<{request<T = unknown>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
const identifier = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && /^[A-Za-z0-9_.-]+$/.test(value);
const optionalUrl = (value: unknown) => typeof value === 'string' && value.length > 0 && value.length <= 4096 ? value : undefined;

export async function readTitleIdentity(api: Requester, itemId: string, signal?: AbortSignal): Promise<TitleIdentity> {
  const raw = await api.request<Record<string, unknown>>(encodedRoutePath('get_items_id_detail', {id: encodeURIComponent(itemId)}) + '?related=none', 'GET', undefined, signal);
  const item = raw && typeof raw === 'object' ? raw.item as Record<string, unknown> | undefined : undefined;
  const scope = raw && typeof raw === 'object' ? raw.scope as Record<string, unknown> | undefined : undefined;
  if (!item || !scope || item.id !== itemId || scope.itemId !== itemId || !identifier(item.libraryId) || scope.libraryId !== item.libraryId || !identifier(item.kind)
    || item.title !== undefined && typeof item.title !== 'string') throw new Error('Invalid title identity');
  const posterUrl = optionalUrl(item.posterUrl), backdropUrl = optionalUrl(item.backdropUrl);
  return Object.freeze({id: itemId, libraryId: item.libraryId, kind: item.kind, title: (item.title as string | undefined) ?? '', ...(posterUrl ? {posterUrl} : {}), ...(backdropUrl ? {backdropUrl} : {})});
}
