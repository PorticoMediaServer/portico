/**
 * The event side of a Portico module written in JavaScript. On Apple these modules are
 * `RCTEventEmitter`s and the shared code listens with `new NativeEventEmitter(module)`; the
 * `react-native` shim hands such listeners to the module's `ModuleEvents` instead, so event names
 * and bodies stay exactly the Apple ones.
 */
export type Subscription = {remove(): void};
type Listener = (body: any) => void;

export class ModuleEvents {
  private readonly listeners = new Map<string, Set<Listener>>();
  /** Called when the first listener for any event arrives and when the last one leaves. */
  constructor(private readonly observing?: (active: boolean) => void) {}

  addListener(name: string, listener: Listener): Subscription {
    const before = this.total();
    let set = this.listeners.get(name);
    if (!set) this.listeners.set(name, (set = new Set()));
    set.add(listener);
    if (before === 0) this.observing?.(true);
    return {
      remove: () => {
        if (!set!.delete(listener)) return;
        if (set!.size === 0) this.listeners.delete(name);
        if (this.total() === 0) this.observing?.(false);
      },
    };
  }

  emit(name: string, body?: unknown): void {
    const set = this.listeners.get(name);
    if (!set) return;
    // A listener that throws must not keep the others from hearing the event.
    for (const listener of [...set]) {
      try { listener(body); } catch (error) { console.error(`A ${name} listener failed.`, error); }
    }
  }

  count(name: string): number { return this.listeners.get(name)?.size ?? 0; }

  removeAll(name: string): void {
    if (!this.listeners.delete(name)) return;
    if (this.total() === 0) this.observing?.(false);
  }

  private total(): number {
    let n = 0;
    for (const set of this.listeners.values()) n += set.size;
    return n;
  }
}

const emitters = new WeakMap<object, ModuleEvents>();

/** Marks `module` as one whose events come from `events`. */
export function withEvents<T extends object>(module: T, events: ModuleEvents): T {
  emitters.set(module, events);
  return module;
}

export function eventsOf(module: unknown): ModuleEvents | undefined {
  return module && typeof module === 'object' ? emitters.get(module) : undefined;
}

/** A rejection shaped like a native module's: an Error carrying the module's `code`. */
export function failure(code: string, message: string): Error & {code: string} {
  return Object.assign(new Error(message), {code});
}
