import type {ProfileSettingsSource} from './source';

/** Subscribe at the route lifetime, before a failed write can clear the
 * document and unmount the list/dialog. Report each error once for this source. */
export function observeProfileFailures(source: ProfileSettingsSource, isCurrent: () => boolean, onFailure: () => void): () => void {
  let closed = false, reported: unknown;
  const read = () => {
    if (closed || !isCurrent()) return;
    const error = source.getSnapshot().error;
    if (!error || error === reported) return;
    reported = error;onFailure();
  };
  const off = source.subscribe(read);read();
  return () => {closed = true;off();};
}
