/**
 * When the restore dialog may send. Every restore asks for the typed word (console rule 60), typed exactly;
 * the owner's password and two-step code are checked by the server, so the page only asks that a password
 * was given. A backup that has not passed its check is never offered.
 */
export function restoreReady(input: Readonly<{checked: boolean; busy: boolean; password: string; typed: string; word: string}>): boolean {
  return input.checked && !input.busy && input.password.length > 0 && input.typed === input.word;
}

/** A backup this server did not make is sent as confirmed once its warning is on screen: the check said so,
 * or the server said so on the first press. */
export function sentAsConfirmed(input: Readonly<{madeHere: boolean; warned: boolean}>): boolean {
  return !input.madeHere || input.warned;
}
