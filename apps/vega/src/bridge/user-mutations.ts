/**
 * The shared shell tells the system audio player which sign-in is active and clears its finished
 * stops (apps/native `platform/user-mutations.ts`). Vega has no such player and so no stops of its
 * own to keep: there is nothing to store, and every question has the empty answer.
 */
export function userMutationTransaction(operation: string, _json: string): Promise<string | boolean> {
  return Promise.resolve(operation === 'read' ? JSON.stringify({version: 1, revision: 0, entries: []}) : true);
}
