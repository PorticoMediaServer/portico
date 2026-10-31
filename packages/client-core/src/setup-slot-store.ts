import {parseSetupAttempt, parseSetupRecord, readSetupLocal, isUnreadable, type SetupAttempt, type SetupLocal, type SetupRecord, type SetupStore} from './setup-store.ts';

/** One protected record: a read, and a compare-and-swap change that sees damaged bytes as absent. */
export type SetupSlot = Readonly<{assertAvailable: () => void; read: () => Promise<unknown>; change: (update: (old: unknown) => unknown) => Promise<void>}>;
export type SetupSlots = Readonly<{record: SetupSlot; attempt: SetupSlot}>;

class Refused extends Error {}
/** Bytes this device saved but can never read again (the protected store's own code). */
const damaged = (error: unknown) => (error as {code?: unknown} | null)?.code === 'invalid_saved_connection';

/** One server's setup records on this device (B-DESIGN §6.3), over two protected slots: the record (per browser, so another
 * tab sees the setup in progress, or per app) and the attempt (the PKCE half; per tab on the web). Each slot is its own
 * protected store, written by compare-and-swap; a change names one slot at a time, so no write spans the two. Damaged bytes read as unreadable,
 * and the protected store's own `change` sees them as absent, so retiring them needs no special case. */
export class SlotSetupStore implements SetupStore {
  private readonly serverId: string;
  private readonly slots: SetupSlots;
  constructor(serverId: string, slots: SetupSlots) {this.serverId = serverId; this.slots = slots;}
  assertAvailable() {this.slots.record.assertAvailable(); this.slots.attempt.assertAvailable();}
  private async raw(slot: SetupSlot): Promise<unknown> {
    try {return await slot.read();} catch (error) {if (damaged(error)) return {unreadable: true}; throw error;}
  }
  async read(): Promise<SetupLocal> {
    const [record, attempt] = await Promise.all([this.raw(this.slots.record), this.raw(this.slots.attempt)]);
    // Another server's attempt in this slot is not this server's: it stays, and reads as none here.
    const parsedAttempt = attempt === undefined ? undefined : parseSetupAttempt(attempt);
    const local = readSetupLocal(record, parsedAttempt && !isUnreadable(parsedAttempt) && parsedAttempt.serverId !== this.serverId ? undefined : attempt);
    const recordOk = !local.record || local.record.serverId === this.serverId;
    return recordOk ? local : Object.freeze({...(local.attempt ? {attempt: local.attempt} : {}), unreadable: true as const});
  }
  async change(expect: Readonly<{requestId?: string; attemptId?: string}>, next: Readonly<{record?: SetupRecord | null; attempt?: SetupAttempt | null}>): Promise<boolean> {
    if (next.record !== undefined && next.record !== null && next.record.serverId !== this.serverId) return false;
    if (next.attempt !== undefined && next.attempt !== null && next.attempt.serverId !== this.serverId) return false;
    try {
      if (next.attempt !== undefined) await this.slots.attempt.change(old => {
        const current = old === undefined ? undefined : parseSetupAttempt(old);
        const id = current && !isUnreadable(current) ? current.attemptId : undefined;
        if (id !== expect.attemptId) throw new Refused();
        return next.attempt ?? undefined;
      });
      if (next.record !== undefined) await this.slots.record.change(old => {
        const current = old === undefined ? undefined : parseSetupRecord(old);
        const id = current && !isUnreadable(current) ? current.requestId : undefined;
        if (id !== expect.requestId) throw new Refused();
        return next.record ?? undefined;
      });
      return true;
    } catch (error) {
      if (error instanceof Refused) return false;
      throw error;
    }
  }
}
