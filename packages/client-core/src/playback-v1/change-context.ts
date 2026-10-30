/** The caller owns its host/navigation lifetime, not the output generation this
 * change intentionally replaces. Strict changes may never restart an ended session. */
export type SessionChangeContext = Readonly<{
  signal?: AbortSignal;
  isCurrent?: () => boolean;
  recoverEnded?: false;
}>;

export function sessionChangeCurrent(context?: SessionChangeContext): boolean {
  try {return !context?.signal?.aborted && context?.isCurrent?.() !== false;}
  catch {return false;}
}

export function requireSessionChangeCurrent(context?: SessionChangeContext): void {
  if (!sessionChangeCurrent(context)) throw new DOMException('Playback change retired', 'AbortError');
}
