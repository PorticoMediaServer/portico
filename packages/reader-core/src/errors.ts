import type {ReaderErrorCode} from './ports.ts';
export class ReaderError extends Error {
  readonly code: ReaderErrorCode;
  constructor(code: ReaderErrorCode, options?: ErrorOptions) {
    super(code, options); this.name = 'ReaderError'; this.code = code;
  }
}
export function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) throw new ReaderError('aborted');
}
export function readerErrorCode(error: unknown): ReaderErrorCode {
  if (error instanceof ReaderError) return error.code;
  if (error instanceof DOMException && error.name === 'AbortError') return 'aborted';
  return 'render_failed';
}
