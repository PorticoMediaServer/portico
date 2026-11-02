/**
 * PR-1: the server refused this tab's sign-in with profile_pin_manage (a remembered main profile past its
 * management hour). The shell hears it once per refusal, from whichever screen asked, and asks for the PIN.
 * Viewing is untouched; entering the PIN chooses the same profile again, which opens management for the hour.
 */
export type ManageGate = Readonly<{
  /** How many management refusals this shell has heard; a change asks for the PIN. */
  getSnapshot(): number;
  subscribe(listener: () => void): () => void;
}>;

export function createManageGate(): ManageGate & Readonly<{refused(): void}> {
  let refusals = 0;
  const listeners = new Set<() => void>();
  return Object.freeze({
    getSnapshot: () => refusals,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    refused: () => {refusals++; for (const listener of [...listeners]) try {listener();} catch { /* the others still hear it */ }},
  });
}
