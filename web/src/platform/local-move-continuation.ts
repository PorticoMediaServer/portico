import {createBrowserAccountEntry, type BrowserAccountEntry} from './account-entry';
import {AccountSignInForm} from '../screens/accounts/sign-in-form';
import type {LocalMoveCustody} from './local-move';

/** One paired handoff: the private sign-in entry and the one-shot custody that
 * entry must admit. The two travel and retire as one identity, so a stale
 * attempt can only ever close its own pair and never a successor's. */
export class LocalMoveHandoff {
  private closed = false;
  /** The owner asked to set up two-step verification once signed in. */
  constructor(readonly entry: BrowserAccountEntry, readonly form: AccountSignInForm, readonly custody: LocalMoveCustody, readonly twoStep = false) {}
  /** Dispose this pair and retire its custody unless admission already transferred
   * it. Idempotent, so the sign-in screen and a failed navigation may both close. */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.form.dispose();
    this.entry.dispose();
    void this.custody.retire();
  }
}

let pending: LocalMoveHandoff | undefined;

/** Build the private entry/form and publish the pair together. A construction
 * failure retires the fresh custody before rethrowing; a replaced pending pair is
 * closed as itself, never whichever pair happens to be pending later. */
export function offerLocalMove(custody: LocalMoveCustody, choices: Readonly<{twoStep?: boolean}> = {}): LocalMoveHandoff {
  const entry = (() => {
    try {return createBrowserAccountEntry();}
    catch (error) {void custody.retire();throw error;}
  })();
  const form = (() => {
    try {return new AccountSignInForm(entry);}
    catch (error) {entry.dispose();void custody.retire();throw error;}
  })();
  const handoff = new LocalMoveHandoff(entry, form, custody, choices.twoStep === true);
  const previous = pending;pending = handoff;
  previous?.close();
  return handoff;
}

/** The sign-in screen takes the single pending pair; a second take gets nothing. */
export function takeLocalMoveHandoff(): LocalMoveHandoff | undefined {
  const value = pending;pending = undefined;return value;
}

/** Close exactly this pair. A stale attempt's failure cannot touch a successor. */
export function cancelLocalMoveHandoff(handoff: LocalMoveHandoff): void {
  if (pending === handoff) pending = undefined;
  handoff.close();
}
