import {defaultI18n} from '../../../i18n/src/index.ts';

/** The personal fact a selection job sets: one of these per job. */
export type SelectionPersonalArgs = Readonly<{watchlist?: boolean; watched?: boolean; favorite?: boolean}>;

/** What a finished personal job says once its selection has ended (select.html: an action ends the selection
 * and a toast says what happened). A job that reached the server is never reported as cancelled (walk J-14). */
export function selectionDoneNotice(args: SelectionPersonalArgs, failed = 0): string {
  if (failed > 0) return defaultI18n.t('next.selection.donePartial', {failed});
  if (args.watchlist === true) return defaultI18n.t('next.overlays.addedMyList');
  if (args.watchlist === false) return defaultI18n.t('next.overlays.removedMyList');
  if (args.watched === true) return defaultI18n.t('next.selection.doneWatched');
  if (args.watched === false) return defaultI18n.t('next.selection.doneUnwatched');
  if (args.favorite === true) return defaultI18n.t('next.selection.doneFavorite');
  return defaultI18n.t('next.selection.saved');
}
