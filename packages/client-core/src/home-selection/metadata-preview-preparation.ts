import {routePath} from '../../../contracts/src/route-paths.ts';
import type {HomeApi} from '../home.ts';
import type {LibraryContentApi} from '../library-content.ts';
import {browsePreparationError} from '../browse-preparation.ts';
import {readPreparation, PreparationProtocolError, type PreparationResponse, type PreparationRuntime, type PreparingRead} from '../read-preparation.ts';
import {refuseUnknownVariant} from '../tolerant.ts';
import type {BulkMetadataPreviewRequest} from './metadata-preview.ts';

export type MetadataPreviewLifetime = Readonly<{
  signal?: AbortSignal;
  isCurrent?(): boolean;
  onPreparing?(pending: PreparingRead): void;
  runtime?: Partial<PreparationRuntime>;
}>;
const previewVariants = ['ready', 'preparing'] as const;
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);

export function parseMetadataPreviewPreparing(body: unknown, response?: PreparationResponse): PreparingRead {
  refuseUnknownVariant(body, previewVariants);
  if (!object(body) || body.variant !== 'preparing' || body.kind !== 'metadata-preview-preparing' || body.state !== 'preparing'
    || typeof body.queryId !== 'string' || !/^[A-Za-z0-9._~-]{1,128}$/.test(body.queryId) || ['.', '..'].includes(body.queryId)
    || body.retryAfterMs !== 1000) throw new PreparationProtocolError('invalid_preparing', response);
  return Object.freeze({queryId: body.queryId, retryAfterMs: body.retryAfterMs});
}

/** One immutable POST descriptor, paced GET polls and private demand cancellation.
 * Body-only presentation ports still carry the explicit domain pending envelope.
 * Raw ports additionally preserve status, Location, Retry-After and typed refusals. */
export async function readMetadataPreview<Ready>(api: HomeApi, request: BulkMetadataPreviewRequest,
  parser: (body: unknown, captured: BulkMetadataPreviewRequest) => Ready, lifetime: MetadataPreviewLifetime): Promise<Ready> {
  const captured = JSON.parse(JSON.stringify(request)) as BulkMetadataPreviewRequest;
  const freeze = (value: unknown): void => {if (object(value) || Array.isArray(value)) {Object.values(value).forEach(freeze); Object.freeze(value);}};
  freeze(captured);
  const path = routePath('post_metadata_bulk_preview');
  const raw = (api as HomeApi & Pick<LibraryContentApi, 'requestRaw'>).requestRaw?.bind(api);
  const result = await readPreparation({
    api: {requestRaw: async (target, method, options) => {
      const initial = target === path && method === 'GET', verb = initial ? 'POST' : method;
      if (raw) return raw(target, verb, {...options, ...(initial ? {body: captured} : {})});
      const body = await api.request(target, verb, initial ? captured : undefined, options?.signal);
      // Body-only request() rejects non-success HTTP statuses. Never infer ready
      // from a pending envelope or accept another domain's handle for cancellation.
      const pending = object(body) && (body.state === 'preparing' || typeof body.kind === 'string' && body.kind.endsWith('-preparing'));
      if (pending) parseMetadataPreviewPreparing(body);
      return {status: pending ? 202 : 200, headers: {}, body};
    }}, descriptor: captured,
    paths: {initial: () => path, refresh: () => path,
      poll: queryId => routePath('get_metadata_bulk_preview_query', {queryId}),
      cancel: queryId => routePath('delete_metadata_bulk_preview_query', {queryId})},
    parseReady: body => {
      refuseUnknownVariant(body, previewVariants);
      if (!object(body) || body.variant !== 'ready') throw new PreparationProtocolError('invalid_ready_handle');
      return parser(body, captured);
    }, parsePreparing: parseMetadataPreviewPreparing,
    errorFromResponse: browsePreparationError, signal: lifetime.signal,
    isCurrent: lifetime.isCurrent ?? (() => !lifetime.signal?.aborted),
    onPreparing: lifetime.onPreparing, runtime: lifetime.runtime,
  });
  return result.value;
}
