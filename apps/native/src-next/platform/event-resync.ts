/** Native surfaces borrow the existing feed's observational ports. */
type RecoveryContext = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
type Recovery = Readonly<{dispose(): void}>;
type RecoveryEvents = Readonly<{
  onResync(listener: () => void): () => void;
  onResyncRead?(read: (context: RecoveryContext) => Promise<void>, options?: {priority?: number; admission?: 'external'}): Recovery;
  resyncReads?: {run<T>(signal: AbortSignal, read: () => Promise<T>): Promise<T>};
}>;

/** Actual GET admission only; mutations keep their existing priority and receipt owner. */
export function readEventState<T>(events: RecoveryEvents, signal: AbortSignal, read: () => Promise<T>): Promise<T> {
  if (signal.aborted) return Promise.reject(new Error('cancelled'));
  return events.resyncReads ? events.resyncReads.run(signal, read) : read();
}

/** The service's own reads use readEventState, so this callback holds no nested read permit. */
export function recoverEventState(events: RecoveryEvents, read: (context: RecoveryContext) => Promise<void>, options: {priority?: number} = {}): () => void {
  const recovery = events.onResyncRead?.(read, {...options, admission: 'external'});
  if (recovery) return () => recovery.dispose();
  const controller = new AbortController();
  let running = false, pending = false;
  const drain = async () => {
    if (running || controller.signal.aborted) return;
    running = true;
    try {while (pending && !controller.signal.aborted) {pending = false; await read({signal: controller.signal, isCurrent: () => !controller.signal.aborted});}}
    catch {pending = true; /* The service presents its error; the next authorized wake still owes this read. */ }
    finally {running = false;}
  };
  const off = events.onResync(() => {pending = true; void drain();});
  return () => {controller.abort(); off();};
}
