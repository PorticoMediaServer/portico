import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../../platform/playback';
import type {BrowserAudioFacts} from '../../../platform/playback/audio/output';

type AudioFactsPort = Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'audioEngineFacts'>;

/** Read-only observation of this exact rendered local music run. The runtime's
 * accepting port checks the original full fence; the snapshot checks also reject
 * a successor run with an equal fence, and never borrow its output facts. */
export function readWatchMusicAudioFacts(runtime: AudioFactsPort, rendered: WebPlaybackRuntimeSnapshot): BrowserAudioFacts | undefined {
  const fence = rendered.fence;
  if (rendered.disposed || !fence || rendered.presentation?.kind !== 'music' || rendered.session?.kind !== 'audio' ||
    !rendered.playback.audioEffects.rendering || rendered.dock.remote?.phase === 'playing') return undefined;
  try {
    if (runtime.getSnapshot() !== rendered) return undefined;
    const facts = runtime.audioEngineFacts(fence);
    return runtime.getSnapshot() === rendered ? facts : undefined;
  } catch {return undefined;}
}
