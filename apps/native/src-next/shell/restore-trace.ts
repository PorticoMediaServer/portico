/** Timestamped steps of the latest native restore (E-mobile-01). Step names and offsets only: no
 * addresses, identifiers or credentials. A restore that outlives its deadline is reported with
 * the step it was still waiting on. */
let started = 0;
let marks: [string, number][] = [];

export function beginRestoreTrace() {started = Date.now(); marks = [];}
export function markRestore(step: string) {marks.push([step, Date.now() - started]); if (marks.length > 40) marks.shift();}
export function restoreTrace() {return {waiting: marks[marks.length - 1]?.[0] ?? 'start', elapsedMs: started ? Date.now() - started : 0, marks: [...marks]};}
