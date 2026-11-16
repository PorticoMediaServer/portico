/** The facts a player waits on: the playback service's snapshot and whether a problem is already being shown. */
export type PlaybackWaitingFacts = Readonly<{problem?: unknown; playback: Readonly<{recovering?: boolean; intent: string; phase: string; pendingSeek?: unknown; session?: Readonly<{converting?: boolean}>}>}>;
/** The player waits visibly when media stops arriving while playing, and, for a stream the server converts, also while
 * its start or a seek waits for the first piece. `preparing` is the server converting: the line says "Getting ready"
 * (definition 07, rule 3); otherwise it is the connection line. Shared by web, phone and TV. */
export function playbackWaiting(open: boolean, snapshot: PlaybackWaitingFacts): {waiting: boolean; preparing: boolean} {
  const p = snapshot.playback, playing = open && p.intent === 'playing' && p.phase !== 'error' && !snapshot.problem;
  const preparing = playing && p.session?.converting === true && (p.phase === 'starting' || !!p.pendingSeek || p.recovering === true);
  return {waiting: preparing || playing && p.recovering === true, preparing};
}
/** How long a wait lasts before its line is drawn (states.html f23): a short hiccup draws nothing. */
export const playbackWaitingDelayMs = 3000;
