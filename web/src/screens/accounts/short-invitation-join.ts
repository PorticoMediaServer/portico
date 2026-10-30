/** The exact local-password state a captured Join click may leave. A stale
 * callback, a changed entry, or a busy/challenge/return state must do nothing. */
export type ShortInvitationEntrySnapshot = Readonly<{
  phase: string;
  server?: Readonly<{id: string; signIn: string}>;
  origin?: string;
  busy?: boolean;
}>;
export type ShortInvitationFormSnapshot = Readonly<{busy: boolean; factor?: unknown}>;
export function canOpenShortInvitation(
  current: ShortInvitationEntrySnapshot,
  rendered: ShortInvitationEntrySnapshot,
  form: ShortInvitationFormSnapshot,
  alive: boolean,
  joining: boolean,
  initializing: boolean,
): boolean {
  return alive && !joining && !initializing && !form.busy && !form.factor && !current.busy &&
    current.phase === 'password' && current.server?.signIn === 'local' &&
    rendered.phase === 'password' && rendered.server?.signIn === 'local' &&
    current.phase === rendered.phase && current.server?.id === rendered.server?.id && current.origin === rendered.origin;
}
