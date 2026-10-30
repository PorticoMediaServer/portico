/**
 * The one rule for player choices: what is set in Settings applies everywhere; what is changed
 * inside the player (speed, subtitle position, Forced only, picture fill) applies to that
 * playback only. A playback is `PlaybackSnapshot.playbackRun`: it goes on through the queue
 * (autoplay, Up Next, Next and Previous, a restart in place) and ends when the viewer starts
 * something new. Nothing here is written to storage; a held choice from an earlier playback
 * reads as the Settings value.
 */
export class PlayerSessionChoices {
  private held = new Map<string, Readonly<{run: number; value: unknown}>>();
  private listeners = new Set<() => void>();
  private version = 0;
  /** Changes with every choice made, for a screen that follows them (`useSyncExternalStore`). */
  getVersion = () => this.version;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  /** The choice in force for `run`: the one made in the player during it, else `settings`. */
  get<T>(name: string, run: number, settings: T): T {
    const held = this.held.get(name);
    return held && held.run === run ? (held.value as T) : settings;
  }
  /** Whether the player changed `name` during `run`. */
  has(name: string, run: number): boolean { return this.held.get(name)?.run === run; }
  set<T>(name: string, run: number, value: T): void {
    this.held.set(name, Object.freeze({run, value}));
    this.version++;
    for (const listener of this.listeners) listener();
  }
}

/** The app's one set of held choices (the player can be mounted again during a playback). */
export const playerSessionChoices = new PlayerSessionChoices();
