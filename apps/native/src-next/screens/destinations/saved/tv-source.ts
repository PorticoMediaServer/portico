import type {NativeSavedSelectionSource} from '../../saved/selection';
import type {NativeTVSavedController} from './model';

/** The TV Saved landing already reads the two authorized projections, one per shape, for the
 * tab it shows. This adapter presents those exact canonical projections to the same Saved
 * selection owner the phone hub uses — posters as My List's projection and squares as
 * Favorites', both from the current tab — so the selection's view identity, membership and
 * per-leaf grants come from the real server projection rather than a copy. It never rebuilds
 * or trims a projection: the selection's own AV leaf partition filters what it may pick.
 * One frozen source snapshot is kept per controller state, so identity comparisons are safe. */
export function nativeTVSavedSelectionSource(controller: NativeTVSavedController): NativeSavedSelectionSource {
  let cachedState: ReturnType<NativeTVSavedController['getSnapshot']> | undefined;
  let cached: ReturnType<NativeSavedSelectionSource['getSnapshot']> | undefined;
  const read = (): ReturnType<NativeSavedSelectionSource['getSnapshot']> => {
    const state = controller.getSnapshot();
    if (cached && state === cachedState) return cached;
    const route = Object.freeze({view: state.tab});
    const next: ReturnType<NativeSavedSelectionSource['getSnapshot']> = Object.freeze({
      phase: state.phase,
      myList: Object.freeze({route, ...(state.posters ? {projection: state.posters} : {})}),
      favorites: Object.freeze({route, ...(state.squares ? {projection: state.squares} : {})}),
    });
    cachedState = state; cached = next;
    return next;
  };
  return Object.freeze({
    getSnapshot: read,
    subscribe: controller.subscribe,
  });
}
