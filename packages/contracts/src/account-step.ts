/** How long a person may take over a step that leaves for their Portico Account and comes back: a
 * setup, an owner move, a member sign-in return. Creating and email-verifying an account fits in it.
 * The server's `claimweb.AccountStepLifetime` (server/internal/claimweb/setup_start.go) is the same
 * thirty minutes; a server test pins the two together. */
export const ACCOUNT_STEP_WINDOW_MS = 30 * 60_000;
