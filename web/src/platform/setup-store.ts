import {routeBase64, routeBytes} from '@core/route-identity.ts';
import {parseSetupAttempt, parseSetupRecord, readSetupLocal, isUnreadable, type SetupAttempt, type SetupLocal, type SetupRecord, type SetupStore} from '@core/setup-store.ts';
import {setupBrowserPending} from './setup-pending';

type Slot<T> = Pick<ReturnType<typeof setupBrowserPending<T>>, 'assertAvailable' | 'read' | 'change'>;
export type BrowserSetupSlots = Readonly<{record: Slot<unknown>; attempt: Slot<unknown>}>;

class Refused extends Error {}
/** Bytes this browser saved but can never read again (the protected store's own code). */
const damaged = (error: unknown) => (error as {code?: unknown} | null)?.code === 'invalid_saved_connection';

/** One server's setup records in this browser (B-DESIGN §6.3): the record per browser, so another tab sees the setup
 * in progress, and the attempt (the PKCE half) per tab. Each slot is its own protected store, written by
 * compare-and-swap; a change names one slot at a time, so no write spans the two. Damaged bytes read as unreadable,
 * and the protected store's own `change` sees them as absent, so retiring them needs no special case. */
export class BrowserSetupStore implements SetupStore {
  constructor(private readonly serverId: string, private readonly slots: BrowserSetupSlots) {}
  assertAvailable() {this.slots.record.assertAvailable(); this.slots.attempt.assertAvailable();}
  private async raw(slot: Slot<unknown>): Promise<unknown> {
    try {return await slot.read();} catch (error) {if (damaged(error)) return {unreadable: true}; throw error;}
  }
  async read(): Promise<SetupLocal> {
    const [record, attempt] = await Promise.all([this.raw(this.slots.record), this.raw(this.slots.attempt)]);
    // Another server's attempt in this tab is not this server's: it stays, and reads as none here.
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

const TAB_KEY = 'portico.setup.tab.v2';
/** This tab's id: the attempt never leaves the tab that made it. */
function tabId(): string {
  let tab = sessionStorage.getItem(TAB_KEY);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(TAB_KEY, tab);}
  return tab;
}

export function browserSetupStore(serverId: string): BrowserSetupStore {
  return new BrowserSetupStore(serverId, {record: setupBrowserPending<unknown>('portico.setup.record.v2:' + serverId), attempt: setupBrowserPending<unknown>('portico.setup.attempt.v2:' + tabId())});
}
