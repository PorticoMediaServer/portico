import type {PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativeShellNavigation} from '../../shell/routes';
import type {NativePlaybackAdapter} from '../../platform/playback/types';

type Subscribe = (listener: () => void) => () => void;
type Host = Readonly<{
  current: () => boolean; subscribeCurrent: Subscribe;
  navigation: Pick<NativeShellNavigation, 'getSnapshot' | 'subscribe'>;
  playback: Readonly<{
    getSnapshot: () => Readonly<{dock: Readonly<{local: Readonly<{expanded: boolean}> | null}>}>;
    subscribe: Subscribe;
    requestPictureInPicture: NonNullable<NativePlaybackAdapter['requestPictureInPicture']>;
  }>;
}>;

/** The existing Watch host owns this gesture, not its deliberately replaced
 * output generation. There is no Home/card alias or second media controller. */
export class NativeWatchExternalRequests {
  private active = true;
  private controllers = new Set<AbortController>();
  private readonly host: Host;
  private readonly reportError: (error: unknown) => void;
  constructor(host: Host, reportError: (error: unknown) => void) {this.host = host; this.reportError = reportError;}
  request = async (fence: PlaybackFence): Promise<boolean> => {
    const revision = this.host.navigation.getSnapshot().revision;
    const current = () => this.active && this.host.current() && this.host.navigation.getSnapshot().revision === revision
      && this.host.playback.getSnapshot().dock.local?.expanded === true;
    if (!current()) return false;
    for (const previous of this.controllers) previous.abort();
    const controller = new AbortController(); this.controllers.add(controller);
    const check = () => {if (!current()) controller.abort();};
    const unsubscribe = [this.host.navigation.subscribe(check), this.host.playback.subscribe(check), this.host.subscribeCurrent(check)];
    let closed = false;
    const release = () => {if (closed) return; closed = true; for (const off of unsubscribe) off();};
    controller.signal.addEventListener('abort', release, {once: true});
    try {
      if (!current()) return false;
      const issued = await this.host.playback.requestPictureInPicture(fence, {signal: controller.signal, isCurrent: current});
      return issued && !controller.signal.aborted && current();
    } catch (error) {
      if (!controller.signal.aborted && current()) this.reportError(error);
      return false;
    } finally {
      release(); controller.signal.removeEventListener('abort', release); this.controllers.delete(controller); controller.abort();
    }
  };
  dispose = (): void => {
    if (!this.active) return;
    this.active = false; for (const controller of this.controllers) controller.abort(); this.controllers.clear();
  };
}
