import {PlaybackPolicy, type PolicyCommand, type PolicyFrame, type PolicyView} from '../../../../../packages/player-core/src/playback-policy';
import {sameFence, type QueuePlaybackFence} from '../../../../../packages/player-core/src/identity';
import type {SegmentMarker} from '../../../../../packages/player-core/src/segment-markers';
import type {QueuePostPlay} from '../../../../../packages/player-core/src/post-play';
import {queueOccurrenceId} from '../../../../../packages/player-core/src/validation';
import type {Marker} from '../../../../../packages/client-core/src/playback-v1/types';

/** Wire options tolerate optional marker data. Admit only bounded, unambiguous
 * markers to the strict policy; unusable hints must not interrupt the picture. */
export function nativeVideoPolicyMarkers(markers: readonly Marker[], duration: number): readonly SegmentMarker[] {
  if (markers.length > 512) return [];
  const supported = markers.filter(marker => marker.type === 'intro' || marker.type === 'recap' || marker.type === 'credits' || marker.type === 'commercial');
  const ids = new Map<string, number>();
  for (const marker of supported) ids.set(marker.id, (ids.get(marker.id) ?? 0) + 1);
  return supported.flatMap((marker): SegmentMarker[] => {
    const kind = marker.type;
    if (kind !== 'intro' && kind !== 'recap' && kind !== 'credits' && kind !== 'commercial') return [];
    const startSeconds = marker.startMs / 1000, endSeconds = marker.endMs / 1000;
    if (ids.get(marker.id) !== 1 || !/^[A-Za-z0-9_-]{1,256}$/.test(marker.id)
      || !Number.isFinite(startSeconds) || !Number.isFinite(endSeconds) || startSeconds < 0 || endSeconds <= startSeconds || endSeconds > 1e9
      || duration > 0 && endSeconds > duration + .001 || typeof marker.automaticSafe !== 'boolean') return [];
    return [{id: marker.id, kind, startSeconds, endSeconds, automaticSafe: marker.automaticSafe}];
  }).sort((a, b) => a.startSeconds - b.startSeconds);
}

/** An invalid optional finish policy offers no automatic advance. Never fill in
 * client defaults or let tolerant wire parsing manufacture autoplay authority. */
export function nativeVideoPolicyPostPlay(value: QueuePostPlay | undefined): QueuePostPlay | null {
  if (!value || value.nextEntryId !== null && !queueOccurrenceId(value.nextEntryId)
    || !['ready', 'end', 'unavailable'].includes(value.reason) || ![0, 5, 10, 15].includes(value.countdownSeconds)
    || ![value.available, value.autoplay, value.passoutCheckDue, value.startDuringCredits].every(flag => typeof flag === 'boolean')
    || !Number.isSafeInteger(value.automaticAdvances) || value.automaticAdvances < 0 || value.automaticAdvances > 10000
    || value.available && (value.nextEntryId === null || value.reason !== 'ready')) return null;
  return value;
}

export type NativeVideoPolicyFrame = Readonly<{frame: PolicyFrame; markers: readonly SegmentMarker[]}>;
type Ports = Readonly<{
  read: () => NativeVideoPolicyFrame | undefined;
  current: (fence: QueuePlaybackFence) => boolean;
  execute: (command: PolicyCommand) => Promise<void>;
  changed: () => void;
  failed: (fence: QueuePlaybackFence, error: unknown) => void;
  now: () => number;
  setTimer: (run: () => void, ms: number) => unknown;
  clearTimer: (timer: unknown) => void;
}>;

/** One shared policy owner beside the native engine. Chrome paints its view;
 * neither React mounting nor animation clocks can advance the queue. */
export class NativeVideoPlaybackPolicy {
  private readonly policy = new PlaybackPolicy();
  private fence?: QueuePlaybackFence;
  private view?: PolicyView;
  private timer?: unknown;
  private updating = false;
  private disposed = false;
  private advancing = false;
  private readonly ports: Ports;
  constructor(ports: Ports) {this.ports = ports;}
  getSnapshot = (): PolicyView | undefined => this.view;
  private clearTimer(): void {
    if (this.timer !== undefined) this.ports.clearTimer(this.timer);
    this.timer = undefined;
  }
  update = (): void => {
    if (this.disposed || this.updating) return;
    this.updating = true;
    let commands: readonly PolicyCommand[] = [];
    try {
      const input = this.ports.read();
      if (!input || !this.ports.current(input.frame.fence)) {
        if (this.fence) this.policy.stop(this.fence);
        this.fence = undefined; this.view = undefined; this.advancing = false; this.clearTimer();
        return;
      }
      const {frame, markers} = input;
      if (!sameFence(this.fence, frame.fence)) {this.clearTimer(); this.advancing = false;}
      this.fence = frame.fence;
      this.policy.begin(frame.fence, markers, frame.durationSeconds > 0 ? frame.durationSeconds : undefined);
      const result = this.policy.update({...frame, nowMs: this.ports.now()});
      if (!result) return;
      this.view = result.view; commands = result.commands;
      if (commands.some(command => command.kind === 'advance')) this.advancing = true;
      const prompt = this.view.prompt;
      const timed = !!this.view.undo || !this.advancing && (
        frame.playing && prompt?.kind === 'up-next' && prompt.countdown?.automatic
        // Collapsing the player hides the end card, not the server's autoplay.
        || frame.ended && frame.postPlay?.available && frame.postPlay.autoplay && !frame.postPlay.passoutCheckDue && !this.view.replay
        // A quiet player still follows the same credits policy without a card.
        || frame.playing && !frame.expanded && frame.postPlay?.available && frame.postPlay.autoplay && frame.postPlay.startDuringCredits);
      if (!timed) this.clearTimer();
      else if (this.timer === undefined) {
        const fence = frame.fence;
        this.timer = this.ports.setTimer(() => {
          this.timer = undefined;
          if (this.disposed || !this.ports.current(fence)) return;
          this.update(); this.ports.changed();
        }, 1000);
      }
    } finally {this.updating = false;}
    for (const command of commands) {
      if (!this.ports.current(command.fence)) continue;
      void this.ports.execute(command).catch(error => {if (!this.disposed && this.ports.current(command.fence)) this.ports.failed(command.fence, error);});
    }
  };
  dismiss = (fence: QueuePlaybackFence): boolean => {
    if (this.disposed || !this.ports.current(fence) || !this.policy.dismiss(fence)) return false;
    this.update(); this.ports.changed(); return true;
  };
  skip = async (fence: QueuePlaybackFence, markerId: string): Promise<void> => {
    if (this.disposed || !this.ports.current(fence)) return;
    const command = this.policy.skip(fence, markerId);
    this.update(); this.ports.changed();
    if (command && this.ports.current(fence)) await this.ports.execute(command);
  };
  undo = async (fence: QueuePlaybackFence): Promise<void> => {
    if (this.disposed || !this.ports.current(fence)) return;
    const command = this.policy.undoSkip(fence, this.ports.now());
    this.update(); this.ports.changed();
    if (command && this.ports.current(fence)) await this.ports.execute(command);
  };
  advance = async (fence: QueuePlaybackFence, mode: 'manual' | 'still-watching'): Promise<void> => {
    if (this.disposed || !this.ports.current(fence)) return;
    const command = this.policy.advance(fence, mode);
    if (command) this.advancing = true;
    this.update(); this.ports.changed();
    if (command && this.ports.current(fence)) await this.ports.execute(command);
  };
  cancelAutoplay = (fence: QueuePlaybackFence): void => {
    if (this.disposed || !this.ports.current(fence)) return;
    this.policy.cancelAutoplay(fence); this.update(); this.ports.changed();
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.clearTimer();
    if (this.fence) this.policy.stop(this.fence);
    this.fence = undefined; this.view = undefined;
  };
}
