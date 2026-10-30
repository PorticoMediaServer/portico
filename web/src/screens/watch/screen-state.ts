/** The settled playback a screen's state belongs to: one runtime and one fence.playback run.
 * A temporary missing fence is not a change; a quality/generation change keeps the same key. */
export type SettledPlayback = Readonly<{runtime: object; key: string}>;

export function settledPlaybackKey(fencePlayback: unknown): string {
  return fencePlayback === undefined || fencePlayback === null ? '' : JSON.stringify(fencePlayback);
}

export function settledPlaybackChanged(previous: SettledPlayback | undefined, runtime: object, fencePlayback: unknown): boolean {
  const key = settledPlaybackKey(fencePlayback);
  if (!key) return false;
  return !previous || previous.runtime !== runtime || previous.key !== key;
}
