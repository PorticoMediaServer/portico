import {routeBase64, routeBytes} from '@core/route-identity.ts';
import {SlotSetupStore, type SetupSlots} from '@core/setup-slot-store.ts';
import {setupBrowserPending} from './setup-pending';

export type BrowserSetupSlots = SetupSlots;
/** This browser's setup records: the record per browser, the attempt per tab. */
export {SlotSetupStore as BrowserSetupStore};

const TAB_KEY = 'portico.setup.tab.v2';
/** This tab's id: the attempt never leaves the tab that made it. */
function tabId(): string {
  let tab = sessionStorage.getItem(TAB_KEY);
  if (tab !== null) routeBytes(tab, 32);
  else {tab = routeBase64(crypto.getRandomValues(new Uint8Array(32))); sessionStorage.setItem(TAB_KEY, tab);}
  return tab;
}

/** This tab's attempt, whichever server it is for: what a Return to this tab can finish. */
export const browserAttemptSlot = () => setupBrowserPending<unknown>('portico.setup.attempt.v2:' + tabId());

export function browserSetupStore(serverId: string): SlotSetupStore {
  return new SlotSetupStore(serverId, {record: setupBrowserPending<unknown>('portico.setup.record.v2:' + serverId), attempt: browserAttemptSlot()});
}
