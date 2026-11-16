/** Local listening continuation only. Automatic file removal needs server confirmation. */
export function offlineCompletion(kind: string | undefined, positionSeconds: number, durationSeconds: number, ended: boolean): Readonly<{finished: boolean; resumeSeconds: number}> {
  let finished = ended;
  if (!finished && (kind === 'song' || kind === 'audiobook_file') && Number.isFinite(durationSeconds) && durationSeconds > 0 && Number.isFinite(positionSeconds)) {
    finished = positionSeconds >= durationSeconds - Math.min(30, durationSeconds / 20);
  }
  return {finished, resumeSeconds: ended ? 0 : positionSeconds};
}
