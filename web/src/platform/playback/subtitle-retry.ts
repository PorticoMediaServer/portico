class SubtitleTransportError extends Error {
  constructor(cause: unknown) {super('Subtitle transport failed.', {cause});}
}
const transportError = (error: unknown) => {
  const failure = error as {name?: unknown; status?: unknown} | null;
  return failure?.name === 'AbortError' || typeof failure?.status === 'number' ? error : new SubtitleTransportError(error);
};

/** Mark only failures at the fetch/body boundary. A decoder also throws TypeError
 * for malformed UTF-8, which must not be mistaken for a network outage. The shared
 * readers still own status, size, format, validator and authorization checks. */
export function subtitleFetch(fetcher: typeof fetch): typeof fetch {
  return async (input, init) => {
    let response: Response;
    try {response = await fetcher(input, init);} catch (error) {throw transportError(error);}
    if (!response.ok || !response.body) return response;
    const reader = response.body.getReader();
    let released = false;
    const release = () => {if (!released) {released = true;reader.releaseLock();}};
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          const next = await reader.read();
          if (next.done) {release();controller.close();} else controller.enqueue(next.value);
        } catch (error) {release();controller.error(transportError(error));}
      },
      cancel: reason => reader.cancel(reason).finally(release),
    }, {highWaterMark: 0});
    return new Response(body, {status: response.status, statusText: response.statusText, headers: response.headers});
  };
}

/** Explicit refusals and unreadable documents fail closed; only transport failures retry. */
export function transientSubtitleFailure(error: unknown): boolean {
  const failure = error as {status?: unknown; name?: unknown} | null;
  if (failure?.name === 'AbortError') return false;
  if (typeof failure?.status === 'number') return failure.status >= 500 && failure.status <= 599 || failure.status === 408 || failure.status === 429;
  return error instanceof SubtitleTransportError;
}

/** One selected text document's authorization polling. A transport outage can retain only
 * a document already admitted for this exact selection; a refusal discards its validator too. */
export async function revalidateTextSubtitles<T>(ports: {
  signal: AbortSignal; current(): boolean;
  read(etag: string, signal: AbortSignal): Promise<Readonly<{unchanged: true}> | Readonly<{unchanged: false; document: T; etag: string}>>;
  show(document: T): void; clear(): void; failed(): void; recovered(): void;
  schedule(load: () => void, ms: number): void;
}): Promise<void> {
  let document: T | undefined, etag = '';
  const current = () => !ports.signal.aborted && ports.current();
  const load = async () => {
    if (!current()) return;
    let retryMs = 25_000;
    try {
      const read = await ports.read(etag, ports.signal);
      if (!current()) return;
      if (!read.unchanged) {document = read.document;etag = read.etag;}
      if (document !== undefined) {ports.show(document);ports.recovered();}
    } catch (error) {
      if (!current()) return;
      const transient = transientSubtitleFailure(error);
      if (transient) retryMs = 5000;
      else {document = undefined;etag = '';}
      if (!transient || document === undefined) {ports.clear();ports.failed();}
    } finally {
      if (current()) ports.schedule(() => {void load();}, retryMs);
    }
  };
  await load();
}

/** One selected drawn key/epoch owns this bounded retry budget and its timer. */
export class DrawnSubtitleRetry {
  private timer?: unknown;
  private attempts = 0;
  private retired = false;
  constructor(private readonly ports: {current(): boolean; retry(): void; setTimer(fn: () => void, ms: number): unknown; clearTimer(timer: unknown): void}) {}
  schedule(error: unknown): boolean {
    if (this.retired || !this.ports.current() || !transientSubtitleFailure(error) || this.timer !== undefined || this.attempts >= 3) return false;
    const delay = [1000, 2000, 5000][this.attempts++];
    this.timer = this.ports.setTimer(() => {
      this.timer = undefined;
      if (!this.retired && this.ports.current()) this.ports.retry();
    }, delay);
    return true;
  }
  /** A retry is already waiting for its timer. */
  get pending(): boolean { return this.timer !== undefined; }
  dispose() {
    this.retired = true;
    if (this.timer !== undefined) this.ports.clearTimer(this.timer);
    this.timer = undefined;
  }
}
