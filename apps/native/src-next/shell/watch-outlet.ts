import {playbackFence, sameFence, type PlaybackFence} from '../../../../packages/player-core/src';
import {sameAuthority, type ShellAuthority} from './model';
import type {NativeShellBounds} from './playback';

export type NativeExpandedVideoSlot = Readonly<{
  authority: ShellAuthority;
  fence: PlaybackFence;
  layoutRevision: number;
  window: Readonly<{width: number; height: number}>;
  /** Measured player pane (layout.surface), including letterbox space; this is
   * not the aspect-fitted image rectangle and never guessed window bounds. */
  frameInWindow: NativeShellBounds;
}>;
export type NativeWatchLease = Readonly<{
  publish: (slot: NativeExpandedVideoSlot) => boolean;
  clear: () => void;
  dispose: () => void;
}>;
export type NativeWatchCurrent = Readonly<{
  authority: ShellAuthority;
  fence?: PlaybackFence;
  window: Readonly<{width: number; height: number}>;
}>;
export interface NativeWatchReader {
  getSnapshot: () => NativeExpandedVideoSlot | undefined;
  subscribe: (listener: () => void) => () => void;
}
export interface NativeWatchOutletPort extends NativeWatchReader {
  register: (authority: ShellAuthority) => NativeWatchLease;
}

/** A measured outlet for the single persistent playback surface. It owns no
 * route, panel, controls, media lease, playback command or screen memory. */
export class NativeWatchOutlet implements NativeWatchOutletPort {
  private slot?: NativeExpandedVideoSlot;
  private owner?: object;
  private alive = true;
  private listeners = new Set<() => void>();
  private readonly current: () => NativeWatchCurrent | undefined;
  constructor(current: () => NativeWatchCurrent | undefined) {this.current = current;}
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = (): NativeExpandedVideoSlot | undefined => this.slot && this.accepts(this.slot) ? this.slot : undefined;
  private accepts(slot: NativeExpandedVideoSlot): boolean {
    const current = this.alive ? this.current() : undefined;
    return !!current?.fence && sameAuthority(slot.authority, current.authority) && sameFence(slot.fence, current.fence)
      && slot.window.width === current.window.width && slot.window.height === current.window.height;
  }
  private changed(): void {for (const listener of [...this.listeners]) listener();}
  /** Called by the genuine output/window subscriptions, never a timer. */
  invalidate = (): void => {if (this.slot && !this.accepts(this.slot)) {this.slot = undefined; this.changed();}};
  register = (authority: ShellAuthority): NativeWatchLease => {
    const owner = {}, current = this.alive ? this.current() : undefined;
    let retired = !current || !sameAuthority(authority, current.authority), revision = -1;
    if (!retired) {
      this.owner = owner;
      if (this.slot) {this.slot = undefined; this.changed();}
    }
    const owns = () => this.alive && !retired && this.owner === owner;
    const clear = () => {if (owns() && this.slot) {this.slot = undefined; this.changed();}};
    return Object.freeze({publish: (input: NativeExpandedVideoSlot): boolean => {
      if (!owns() || !sameAuthority(authority, input.authority) || !Number.isSafeInteger(input.layoutRevision) || input.layoutRevision <= revision) return false;
      const {window, frameInWindow: frame} = input;
      if (![window.width, window.height, frame.x, frame.y, frame.width, frame.height].every(Number.isFinite)
        || window.width <= 0 || window.height <= 0 || frame.width <= 0 || frame.height <= 0
        || frame.x >= window.width || frame.y >= window.height || frame.x + frame.width <= 0 || frame.y + frame.height <= 0) return false;
      let fence: PlaybackFence;
      try {fence = playbackFence(input.fence);} catch {return false;}
      const slot = Object.freeze({authority: Object.freeze({...input.authority}), fence, layoutRevision: input.layoutRevision,
        window: Object.freeze({...window}), frameInWindow: Object.freeze({...frame})});
      if (!this.accepts(slot)) return false;
      revision = input.layoutRevision; this.slot = slot; this.changed(); return true;
    }, clear, dispose: () => {clear(); retired = true; if (this.owner === owner) this.owner = undefined;}});
  };
  dispose = (): void => {this.alive = false; this.owner = undefined; this.slot = undefined; this.changed(); this.listeners.clear();};
}
