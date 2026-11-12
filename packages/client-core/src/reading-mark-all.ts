import {serverPath} from './query/server-api.ts';

export type ReadingMarkAllPair = Readonly<{catalog: number; viewer: number}>;
/** What Mark all read would mark: the library's titles this profile has not read. */
export type ReadingMarkAllProjection = Readonly<{serverId: string; libraryId: string; viewerFence: string; revision: ReadingMarkAllPair; count: number}>;
export type ReadingMarkAllCommand = Readonly<{operationId: string; viewerFence: string; expectedRevision: ReadingMarkAllPair}>;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const bad = (): never => {throw new Error('invalid_reading_mark_all');};

/** What a library's "Mark all read" asks of the rebuilt server: how many of its titles are not
 * read yet (GET), and the job that marks every one of those (POST). The job walks the same view
 * the count read, the unread titles only: a book already read that the person is reading again
 * keeps its place, and nothing already read is written again. */
export const readingMarkAllView = (libraryId: string) => `library=${encodeURIComponent(libraryId)}&kind=book,comic,issue&watched=false`;
export function readingMarkAllPath(method: 'GET' | 'POST', libraryId: string): string | undefined {
  if (!identifier(libraryId)) return undefined;
  return method === 'GET' ? serverPath('GET /v1/items', {query: {library: libraryId, kind: 'book,comic,issue', watched: false, limit: 1, total: true}}) : serverPath('POST /v1/jobs');
}
/** One tap's operation, kept through an uncertain answer so a retry is the same tap. */
export function createReadingMarkAllCommand(proof: ReadingMarkAllProjection, operationId: string): ReadingMarkAllCommand {
  if (!identifier(operationId)) return bad();
  return Object.freeze({operationId, viewerFence: proof.viewerFence, expectedRevision: Object.freeze({...proof.revision})});
}
