/** What the Hosted `/claim` page draws. A session that appears for any reason (this tab's own
 * form, or the verification email opened in another tab) moves on to the approval page, which
 * names the account and still waits for an explicit Approve. The code is never approved here. */
export type ClaimStartView = 'form' | 'approval' | 'open-from-server';
export function claimStartView(input: Readonly<{ownerPending: boolean; signedIn: boolean}>): ClaimStartView {
  if (!input.ownerPending) return 'open-from-server';
  return input.signedIn ? 'approval' : 'form';
}
