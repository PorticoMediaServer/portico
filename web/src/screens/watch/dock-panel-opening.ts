import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src/index.ts';

export type WatchDockPanel = 'subtitles-audio' | 'options';
/** Only the observations needed to keep a panel on its original local playback. */
type Observation = Readonly<{
  disposed: boolean; authority: object; fence?: PlaybackFence;
  presentation?: Readonly<{kind: string; fence: PlaybackFence}>;
  dock: Readonly<{local: Readonly<{kind: string; expanded: boolean; fence: PlaybackFence}> | null; remote: object | null}>;
}>;
type Owner = Readonly<{
  binding: object; authority: object; fence: PlaybackFence; panels: readonly WatchDockPanel[];
  snapshot: () => Observation; isCurrent: () => boolean;
  expand: () => void; show: (panel: WatchDockPanel) => void;
  renew: () => void; reportError: (error: unknown) => void;
}>;
export type WatchDockPanelOffer = Readonly<{
  binding: object; authority: object; fence: PlaybackFence; panels: readonly WatchDockPanel[];
  current: () => boolean; request: (panel: WatchDockPanel) => boolean; retire: () => void;
}>;

/** The screen owns the panel across expansion; each collapsed opening gets a
 * separate, synchronously consumed offer. A retired offer never borrows another. */
export class WatchDockPanelOpening {
  private live = true;
  private opening?: {live: boolean};
  private pending?: {opening: object; panel: WatchDockPanel; expanded: boolean};
  private suspended = false;
  readonly panels: readonly WatchDockPanel[];
  constructor(readonly owner: Owner) {this.panels = Object.freeze([...owner.panels]);}
  current = (): boolean => {
    if (!this.live || !this.owner.isCurrent()) return false;
    const value = this.owner.snapshot();
    return !value.disposed && value.authority === this.owner.authority
      && sameFence(value.fence, this.owner.fence) && value.presentation?.kind === 'video'
      && sameFence(value.presentation.fence, this.owner.fence) && !value.dock.remote
      && value.dock.local?.kind === 'video' && sameFence(value.dock.local.fence, this.owner.fence);
  };
  offer(): WatchDockPanelOffer | undefined {
    if (!this.current() || this.owner.snapshot().dock.local?.expanded || this.pending) return;
    this.suspended = false;
    if (this.opening) this.opening.live = false;
    const opening: {live: boolean} = {live: true};
    this.opening = opening;
    const current = () => opening.live && this.opening === opening && !this.pending
      && this.current() && !this.owner.snapshot().dock.local?.expanded;
    return Object.freeze({binding: this.owner.binding, authority: this.owner.authority,
      fence: this.owner.fence, panels: this.panels, current,
      retire: () => {opening.live = false;},
      request: (panel: WatchDockPanel) => {
        if (!current() || !this.panels.includes(panel)) return false;
        opening.live = false;
        const pending = this.pending = {opening, panel, expanded: false};
        try {this.owner.expand(); this.observe();}
        catch (error) {
          if (this.pending === pending) this.pending = undefined;
          if (this.current()) {this.owner.renew(); this.owner.reportError(error);}
        }
        return true;
      }});
  }
  /** The original runtime/sender can collapse or take over before React commits.
   * Retire that offer synchronously; returning local needs a fresh offer. */
  observe = () => {
    if (!this.current()) {
      if (this.opening) this.opening.live = false;
      this.pending = undefined; this.suspended = true;
      return;
    }
    if (this.suspended) {this.suspended = false; this.owner.renew();}
    const pending = this.pending;
    if (!pending) return;
    if (this.owner.snapshot().dock.local?.expanded) pending.expanded = true;
    else if (pending.expanded) {this.pending = undefined; this.owner.renew();}
  };
  /** Called by the original screen's expanded layout commit, never a timer or
   * a lookup of a successor's panel owner. */
  commitExpanded() {
    const pending = this.pending;
    if (!pending) return;
    if (!this.current()) {this.observe(); return;}
    if (!this.owner.snapshot().dock.local?.expanded) return;
    this.pending = undefined;
    try {this.owner.show(pending.panel);}
    catch (error) {if (this.current()) this.owner.reportError(error);}
  }
  dispose() {
    this.live = false;
    if (this.opening) this.opening.live = false;
    this.pending = undefined;
  }
}
