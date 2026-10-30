import type {V1Http, V1Request, V1Response} from '../../../../../packages/client-core/src/playback-v1/http';

/** Cancels the previous principal's work and refuses both late responses and new sends. */
export class PlaybackScopeHttp implements V1Http {
  private closed = false;
  private controllers = new Set<AbortController>();
  private guards = new WeakMap<AbortSignal, () => boolean>();
  private inner: V1Http;
  private current: () => boolean;
  private retiredResponse?: (request: V1Request, response: V1Response) => void;
  constructor(inner: V1Http, current: () => boolean, retiredResponse?: (request: V1Request, response: V1Response) => void) {
    this.inner = inner; this.current = current; this.retiredResponse = retiredResponse;
  }
  /** A caller-owned operation fence follows this exact signal through the core's reads. Each
   * in-flight send retains its guard even after the caller removes the registration. */
  guard(signal: AbortSignal, current: () => boolean): () => void {
    this.guards.set(signal, current);
    return () => { if (this.guards.get(signal) === current) this.guards.delete(signal); };
  }
  /** Retain this exact caller's guard before another wrapper joins its signal. */
  capture(signal?: AbortSignal): (() => boolean) | undefined {return signal ? this.guards.get(signal) : undefined;}
  /** A specialized transport (multipart uploads) shares this exact lifetime and guard. */
  send = async (request: V1Request, transport: V1Http = this.inner) => {
    const guard = request.signal ? this.guards.get(request.signal) : undefined;
    const current = () => !this.closed && this.current() && (!guard || guard());
    if (!current() || request.signal?.aborted) throw new Error('Playback authority retired.');
    const controller = new AbortController();
    const abort = () => controller.abort();
    request.signal?.addEventListener('abort', abort, {once: true});
    if (request.signal?.aborted) controller.abort();
    this.controllers.add(controller);
    try {
      const response = await transport.send({...request, signal: controller.signal});
      if (!current() || controller.signal.aborted) {
        // A transport may finish before its owner receives an authority notification.
        // Retire this exact request before releasing its cancellation registration.
        controller.abort();
        // A cancelled create can still return a genuine resource. Its owner may clean up
        // that resource under its original principal; the response still never reaches callers.
        try { this.retiredResponse?.(request, response); } catch { /* Retirement must still reject. */ }
        throw new Error('Playback authority retired.');
      }
      return response;
    } finally { request.signal?.removeEventListener('abort', abort); this.controllers.delete(controller); }
  };
  dispose(): void {
    this.closed = true;
    for (const controller of this.controllers) controller.abort();
    this.controllers.clear();
  }
}
