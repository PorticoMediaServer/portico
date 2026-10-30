const terminalCodes = new Set(['unauthorized', 'authentication_required', 'session_expired', 'invalid_token', 'unauthenticated', 'forbidden', 'not_permitted', 'permission_changed', 'access_revoked', 'owner_required']);
export function errorFacts(error: unknown) {
  const v = error && typeof error === 'object' ? error as Record<string, unknown> : {};
  const normalized = typeof v.code === 'string' ? v.code.trim().toLowerCase().replace(/[ -]/g, '_') : '';
  const code = /^[a-z_]{1,128}$/.test(normalized) ? normalized : 'invalid_response';
  const status = typeof v.status === 'number' ? v.status : typeof v.statusCode === 'number' ? v.statusCode : undefined;
  return Object.freeze({code, ...(status === undefined ? {} : {status})});
}
export function terminal(error: unknown) {const v = errorFacts(error), raw = error && typeof error === 'object' ? error as Record<string, unknown> : {};return [raw.status, raw.statusCode].some(status => status === 401 || status === 403) || terminalCodes.has(v.code);}
export type Attempt = Readonly<{generation: number; abort: AbortController}>;
/** getSnapshot never returns a retired viewer's retained state. Disposal
 * clears before notifying, including reentrant subscribers. */
export class OwnerStore<S> {
  protected snapshot: S;
  private listeners = new Set<() => void>();
  private disposed = false;
  private generation = 0;
  private attempt?: Attempt;
  constructor(private empty: S, private current: () => boolean, protected reportError: (error: unknown) => void) {this.snapshot = empty;}
  isActive = () => {if (!this.disposed && !this.current()) this.dispose();return !this.disposed;};
  getSnapshot = () => this.isActive() ? this.snapshot : this.empty;
  subscribe = (listener: () => void) => {if (!this.disposed) this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  protected publish(value: S) {if (!this.isActive()) return;this.snapshot = value;for (const listener of [...this.listeners]) {if (!this.isActive()) break;listener();}}
  protected begin(): Attempt {this.attempt?.abort.abort();const attempt = {generation: ++this.generation, abort: new AbortController()};this.attempt = attempt;return attempt;}
  protected valid(attempt: Attempt) {return this.isActive() && attempt.generation === this.generation && !attempt.abort.signal.aborted;}
  protected invalidate() {++this.generation;this.attempt?.abort.abort();}
  protected fail(error: unknown, value: S) {if (terminal(error)) {this.dispose();this.reportError(errorFacts(error));}else this.publish(value);}
  dispose() {if (this.disposed) return;this.disposed = true;this.invalidate();this.snapshot = this.empty;for (const listener of [...this.listeners]) listener();this.listeners.clear();}
}
