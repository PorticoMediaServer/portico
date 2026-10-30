/** Orders native continuation storage only within this JavaScript runtime. */
const pending = new Map<string, Promise<void>>();
export function serializeContinuation<T>(origin: string, work: () => Promise<T>): Promise<T> {
  const key = new URL(origin).origin;
  const previous = pending.get(key) ?? Promise.resolve();
  const result = previous.then(work);
  const settled = result.then(() => {}, () => {});
  pending.set(key, settled);
  void settled.then(() => {if (pending.get(key) === settled) pending.delete(key);});
  return result;
}

/** A witness is shared only by owners that observed the same binding in this runtime. */
type BindingWitness = Readonly<{binding: string}>;
const bindings = new Map<string, BindingWitness>();
export function observeContinuationBinding(origin: string, binding: string): BindingWitness {
  const key = new URL(origin).origin, existing = bindings.get(key);
  if (existing?.binding === binding) return existing;
  const witness = Object.freeze({binding}); bindings.set(key, witness); return witness;
}
export function replaceContinuationBinding(origin: string, binding: string): BindingWitness {
  const witness = Object.freeze({binding}); bindings.set(new URL(origin).origin, witness); return witness;
}
export function continuationBindingCurrent(origin: string, witness: BindingWitness): boolean {
  return bindings.get(new URL(origin).origin) === witness;
}
