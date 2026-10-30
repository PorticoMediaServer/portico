import type {NativePlaybackStart} from '../../platform/playback/types';

type Ports = Readonly<{
  current: () => boolean;
  revision: () => number;
  start: (input: NativePlaybackStart) => Promise<void>;
}>;

/** A title/card owns only its pending start lifetime. A newly accepted player
 * output is not part of this predicate and cannot cancel its own adoption. */
export class NativeTitleStarts {
  private readonly ports: Ports;
  private readonly pending = new Map<AbortController, () => boolean>();
  private disposed = false;
  constructor(ports: Ports) {this.ports = ports;}
  invalidate = () => {for (const [controller, current] of this.pending) if (!current()) controller.abort();};
  start = async (input: NativePlaybackStart, memberCurrent: () => boolean): Promise<void> => {
    const revision = this.ports.revision(), controller = new AbortController();
    const current = () => !this.disposed && this.ports.current() && this.ports.revision() === revision && memberCurrent();
    if (!current()) return;
    this.pending.set(controller, current);
    try {await this.ports.start({...input, lifetime: {signal: controller.signal, isCurrent: current}});}
    finally {this.pending.delete(controller);}
  };
  dispose = () => {if (this.disposed) return; this.disposed = true; for (const controller of this.pending.keys()) controller.abort(); this.pending.clear();};
}
