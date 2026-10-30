import type {DirectorySnapshot} from '../../services/directory-flow';

/** What notice the chooser shows for its current error, if any (H12, 29 Oct).
 * - An account notice only for a failure of Portico Accounts' own requests.
 * - A server's failure is that server's: never worded as the account's.
 * - A state frame that already says what is happening (waiting, unreachable, waiting for Portico
 *   Accounts, a changed identity, a clock, an update) adds no banner that repeats or contradicts it. */
export function directoryNotice(state: Pick<DirectorySnapshot, 'error' | 'errorSource' | 'status'>, place: 'frame' | 'list', server?: string):
  Readonly<{source: 'account' | 'server'; error: unknown; server?: string}> | undefined {
  if (!state.error) return undefined;
  if (state.status && (place === 'frame' || state.status !== 'identity' && state.status !== 'clock' && state.status !== 'update')) return undefined;
  return state.errorSource === 'account' ? {source: 'account', error: state.error} : {source: 'server', error: state.error, server};
}
