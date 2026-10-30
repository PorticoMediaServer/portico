import type {MusicMixesBinding} from './music-mixes-binding';

type ReadBinding = Pick<MusicMixesBinding, 'getSnapshot' | 'load'>;
/** Only this committed row may report its optional read. Releasing the view
 * does not cancel or dispose the profile-owned source another view may share. */
export function readMusicMixesForView(binding: ReadBinding, reportError: (error: unknown) => void): () => void {
  let live = true;
  const admitted = binding.getSnapshot();
  void Promise.resolve().then(() => {
    if (!live || binding.getSnapshot().phase !== 'idle') return;
    return binding.load();
  }).catch(error => {
    if (!live) return;
    const state = binding.getSnapshot();
    if (state.phase === 'retired') return;
    if (typeof error === 'object' && error !== null && 'name' in error && error.name === 'AbortError') return;
    // A superseding read may have settled in the same shared source. Its state
    // cannot authorize the old rejection; the source must have published this error.
    if (state.phase === 'error' && state.error === error || state === admitted && state.phase === 'idle') reportError(error);
  });
  return () => {live = false;};
}
