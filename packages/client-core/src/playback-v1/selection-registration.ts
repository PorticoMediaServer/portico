import type {Session} from './types.ts';

/** A process-private receipt for one exact registration, including known absence.
 * Re-registering the same id/generation retires the receipt; it is not an id lease. */
export type QueueSelectionRegistration = Readonly<{
  current(): boolean;
  forget(isUnowned: () => boolean): boolean;
}>;
export type QueuedSelectionRetirer = (session: Session, isUnowned: () => boolean) => boolean | Promise<boolean>;

export class QueueSelectionRegistrations {
  private readonly tokens = new Map<string, object>();
  changed(id: string): void {
    this.tokens.delete(id); this.tokens.set(id, {});
    while (this.tokens.size > 128) this.tokens.delete(this.tokens.keys().next().value!);
  }
  capture(id: string, isOriginal: () => boolean, forgetOriginal: () => boolean): QueueSelectionRegistration {
    if (!this.tokens.has(id)) this.changed(id);
    const token = this.tokens.get(id)!;
    const current = () => this.tokens.get(id) === token && isOriginal();
    return Object.freeze({current, forget: (isUnowned: () => boolean) => current() && isUnowned() && forgetOriginal()});
  }
}

/** No raw stop/forget fallback: refusal, throw, unknown ownership or a changed
 * registration leaves its owner untouched. The original principal owns retirement. */
export async function retireQueuedSelectionRegistration(session: Session, registration: QueueSelectionRegistration | undefined,
  retire: QueuedSelectionRetirer | undefined, isUnowned: () => boolean): Promise<boolean> {
  const safe = () => {try {return registration?.current() === true && isUnowned();} catch {return false;}};
  if (!registration || !retire || !safe()) return false;
  try {
    if (await retire(session, safe) !== true || !safe()) return false;
    return registration.forget(safe);
  } catch {return false;}
}
