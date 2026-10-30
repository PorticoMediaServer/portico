import {sameAuthority, type ShellAuthority} from '../../shell/model';

export type NativeScreenAuthority = Readonly<{
  authority: () => ShellAuthority;
  current: () => boolean;
  subscribeAuthority: (listener: () => void) => () => void;
}>;

export function screenScopeEnded(): Error {
  return Object.assign(new Error('native_screen_scope_ended'), {name: 'AbortError'});
}

/** One shell lifetime. Guide, Console and Preferences validate their own response fences. */
export class NativeScreenLifetime {
  readonly authority: ShellAuthority;
  readonly signal: AbortSignal;
  private abort = new AbortController();
  private off: () => void = () => {};
  private disposed = false;
  private owner: NativeScreenAuthority;
  constructor(owner: NativeScreenAuthority) {
    this.owner = owner;
    this.authority = Object.freeze({...owner.authority()});
    this.signal = this.abort.signal;
    this.off = owner.subscribeAuthority(() => {if (!this.current()) this.dispose();});
  }
  current = (): boolean => !this.disposed && this.owner.current() && sameAuthority(this.authority, this.owner.authority());
  assert = (): void => {if (!this.current()) throw screenScopeEnded();};
  /** Also abort when this particular viewport/read leaves the screen. */
  async request<T>(read: (signal: AbortSignal) => Promise<T>, caller?: AbortSignal): Promise<T> {
    this.assert();
    const round = new AbortController();
    const stop = () => round.abort();
    this.signal.addEventListener('abort', stop, {once: true});
    caller?.addEventListener('abort', stop, {once: true});
    if (caller?.aborted || this.signal.aborted) stop();
    try {
      if (round.signal.aborted) throw screenScopeEnded();
      const value = await read(round.signal);
      this.assert();
      if (round.signal.aborted) throw screenScopeEnded();
      return value;
    } finally {
      this.signal.removeEventListener('abort', stop);
      caller?.removeEventListener('abort', stop);
    }
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.abort.abort();
    this.off();
  }
}

/** Immutable snapshots suitable for useSyncExternalStore. No successful optimistic writes. */
export class NativeScreenStore<T> {
  private listeners = new Set<() => void>();
  private state: T;
  constructor(state: T) {this.state = state;}
  getSnapshot = (): T => this.state;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  protected publish(state: T): void {this.state = Object.freeze(state); for (const listener of [...this.listeners]) listener();}
  protected clearListeners(): void {this.listeners.clear();}
}
