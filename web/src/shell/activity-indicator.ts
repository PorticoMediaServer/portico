import type {ActivityPanelView} from '../kit/shell';

/** The rail retains the same facts as the top-bar glyph. Unknown health is never
 * projected as a quiet/healthy ready summary. */
export function activityIndicator(view: ActivityPanelView) {
  const known = view.phase === 'ready';
  const count = known ? view.streamCount : undefined;
  const update = known && view.update?.available === true;
  const majorIssue = known ? view.majorIssue : undefined;
  const busy = known && ((count ?? 0) > 0 || view.scans.length > 0 || (view.taskCount ?? 0) > 0);
  return {count, update, majorIssue, busy, dot: majorIssue ? 'major' as const : update ? 'update' as const : undefined};
}
