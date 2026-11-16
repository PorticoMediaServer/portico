/** The server's setup phases a caller with its own setup token can see (server setupflow). */
export const setupPhases = ['started', 'awaiting_approval', 'expired', 'approved', 'finishing', 'exhausted', 'installed', 'installed_lapsed'] as const;
export type SetupPhase = typeof setupPhases[number];

export type SetupOperation = Readonly<{operationId: string; localGeneration: string; revision: string}>;
/** The server applying an approval it was given: since the approval, failed tries, the next one. */
export type SetupDelivery = Readonly<{attempts: number; slow: boolean; since?: string; nextRetryAt?: string}>;
export type OwnSetup = Readonly<{
  phase: SetupPhase; requestId: string; revision: string;
  /** Wall-clock end of the approval window (started, awaiting approval) or the owner admission window (installed). */
  expiresAt?: string;
  operation?: SetupOperation; cancelling: boolean; renewable: boolean;
  delivery?: SetupDelivery;
  /** Started again because its Portico Account declined the last request. */
  outcome?: 'denied';
}>;
/** Anyone sees `status`; `setup` is present only for the token of the Portico Account setup in progress.
 * `setup: 'unknown'` is a phase this app doesn't know: a newer server, shown honestly, never guessed at. */
export type SetupState = Readonly<{
  /** `unknown` is a status from a newer server: shown as "needs a newer app", never guessed at. */
  status: 'ready' | 'in_progress' | 'complete' | 'unknown';
  retryAfterSeconds?: number;
  setup?: OwnSetup | 'unknown';
}>;
