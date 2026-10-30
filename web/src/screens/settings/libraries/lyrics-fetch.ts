import type {LibraryContentApi} from '@core/library-content.ts';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes.ts';

type Receipt = Readonly<{id: string; state: string}>;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const states = new Set(['queued', 'running', 'cancellation-requested', 'reconciling', 'paused', 'succeeded', 'failed', 'cancelled']);
/** One explicit library command. An uncertain result keeps its key for a manual retry. */
export class LibraryLyricsFetch {
  private key: string | undefined;
  private pending: Promise<Receipt | undefined> | undefined;
  private readonly lifetime = new AbortController();
  constructor(private readonly options: Readonly<{api: LibraryContentApi; libraryId: string; isCurrent: () => boolean; randomId: () => string}>) {
    if (!options.libraryId || options.libraryId.length > 256 || /[\x00-\x1f\x7f]/.test(options.libraryId)) throw new Error('invalid_library_id');
  }
  dispose() {this.lifetime.abort(); this.key = undefined;}
  run(): Promise<Receipt | undefined> {
    if (this.lifetime.signal.aborted || !this.options.isCurrent()) return Promise.resolve(undefined);
    if (this.pending) return this.pending;
    // Freeze the key before issuing I/O; generator failures never issue a request.
    if (!this.key) {
      try {
        const key = this.options.randomId();
        if (!/^[A-Za-z0-9_-]{16,128}$/.test(key)) throw new Error('invalid_operation_id');
        this.key = key;
      } catch (error) {return Promise.reject(error);}
    }
    const key = this.key;
    const request = async (): Promise<Receipt | undefined> => {
      try {
        const raw: unknown = await this.options.api.request(encodedRoutePath('post_libraries_id_lyrics_fetch', {id: encodeURIComponent(this.options.libraryId)}), 'POST', {idempotencyKey: key}, this.lifetime.signal);
        if (this.lifetime.signal.aborted || !this.options.isCurrent()) return undefined;
        if (!object(raw) || typeof raw.id !== 'string' || !raw.id || raw.id.length > 256 || /[\x00-\x1f\x7f]/.test(raw.id) ||
            raw.kind !== 'library-lyrics-fetch' || raw.resource !== this.options.libraryId || typeof raw.state !== 'string' || !states.has(raw.state)) throw new Error('invalid_lyrics_job');
        this.key = undefined;
        return {id: raw.id, state: raw.state};
      } catch (error) {
        if (this.lifetime.signal.aborted || !this.options.isCurrent()) return undefined;
        // Only definite refusal permits a new operation. Timeouts, malformed receipts
        // and server errors may follow acceptance and must keep the original key.
        if (object(error) && [400, 401, 403, 404, 422].includes(error.status as number)) this.key = undefined;
        throw error;
      }
    };
    const promise = request().finally(() => {if (this.pending === promise) this.pending = undefined;});
    this.pending = promise;
    return promise;
  }
}
