/** A library collection's owner edits (desktop-detail f8: Rename, Add a film, Change order) on the rebuilt server. */
import {callServer, type ServerRequester} from './query/server-api.ts';

/** Rename the collection, add titles and put titles in order (`PATCH /v1/admin/collections/{id}`). Each part is
 * optional and safe to send twice. */
export async function editLibraryCollection(api: ServerRequester, id: string, edit: Readonly<{title?: string; add?: readonly string[]; order?: readonly string[]}>, signal?: AbortSignal): Promise<void> {
  await callServer(api, 'PATCH /v1/admin/collections/{id}', {id, body: {...(edit.title !== undefined ? {title: edit.title} : {}), ...(edit.add ? {add: [...edit.add]} : {}), ...(edit.order ? {order: [...edit.order]} : {})}, ...(signal ? {signal} : {})});
}

export type CollectionCandidate = Readonly<{id: string; title: string; year?: number; posterUrl?: string}>;
/** The films a search finds in one library (`GET /v1/search?kind=movie`), for adding to that library's collection. */
export async function searchLibraryFilms(api: ServerRequester, q: string, libraryId: string, signal?: AbortSignal): Promise<readonly CollectionCandidate[]> {
  const answer = await callServer(api, 'GET /v1/search', {query: {q, kind: 'movie', limit: 20}, ...(signal ? {signal} : {})}) as unknown as
    Readonly<{groups?: readonly Readonly<{kind?: string; items?: readonly Readonly<{id?: string; title?: string; year?: number; libraryId?: string; images?: Readonly<{poster?: string}>}>[]}>[]}>;
  return (answer?.groups ?? []).filter(group => group.kind === 'movie').flatMap(group => group.items ?? [])
    .filter(item => item.id && item.title && item.libraryId === libraryId)
    .map(item => ({id: item.id!, title: item.title!, ...(item.year ? {year: item.year} : {}), ...(item.images?.poster ? {posterUrl: item.images.poster} : {})}));
}
