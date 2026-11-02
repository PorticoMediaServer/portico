/** SR-8: the generation of an answer the server says it recorded in this viewer's history, if it is
 *  one not seen before. Recent searches reads again once per such answer, never per keystroke or
 *  continuation, and never on the client's own say-so (another device may have turned history off). */
export function newlyRecorded(last: number | undefined, snapshot: Readonly<{phase: string; generation: number; data?: Readonly<{query: Readonly<{recorded: boolean}>}> | null}>): number | undefined {
  return snapshot.phase === 'ready' && snapshot.data?.query.recorded === true && snapshot.generation !== last ? snapshot.generation : undefined;
}
