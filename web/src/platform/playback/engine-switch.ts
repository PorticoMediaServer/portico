import type {PlayerEvent, PlayerSource} from '@core/playback-v1/port.ts';
import type {AirPlaySource} from './airplay.ts';
import type {WebPlayerNotice} from './web-media-player.ts';
import type {PlaybackMediaPort} from './web-playback.ts';

/**
 * A device that carries a second player (the desktop app's mpv) plays each presentation in the
 * one the server planned it for (`presentation.engine`): mpv only where it saves converting or a
 * burn, the page's own player for everything else (playback rule 15). Everything the session
 * core asks goes to the player that holds the current presentation; the other stays unloaded.
 */
export class EngineSwitch implements PlaybackMediaPort {
  private active: PlaybackMediaPort;
  private readonly listeners = new Set<(event: PlayerEvent) => void>();
  private readonly offs: (() => void)[];
  constructor(private readonly own: PlaybackMediaPort, private readonly second: PlaybackMediaPort) {
    this.active = own;
    this.offs = [own, second].map(player => player.onEvent(event => {
      if (player === this.active) for (const listener of [...this.listeners]) listener(event);
    }));
  }
  load(source: PlayerSource): void {
    const next = source.engine === 'mpv' ? this.second : this.own;
    if (next !== this.active) { this.active.unload(); this.active = next; }
    next.load(source);
  }
  play(): void { this.active.play(); }
  pause(): void { this.active.pause(); }
  seek(positionMs: number): void { this.active.seek(positionMs); }
  setRate(rate: number): void { this.active.setRate(rate); }
  selectSidecarSubtitle(trackId: string | null): void { this.active.selectSidecarSubtitle(trackId); }
  /** AirPlay is the page's own player's (Safari's engine in the macOS app). */
  setAirPlaySource(source: AirPlaySource | null): void { this.own.setAirPlaySource?.(source); }
  observe() { return this.active.observe(); }
  onEvent(listener: (event: PlayerEvent) => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  onNotice(listener: (notice: WebPlayerNotice) => void): () => void {
    const offs = [this.own.onNotice(listener), this.second.onNotice(listener)];
    return () => { for (const off of offs) off(); };
  }
  get loaded(): number { return this.active.loaded; }
  get settling(): boolean { return this.active.settling; }
  unload(): void { this.active.unload(); }
  suspend(): void { this.active.suspend(); }
  resume(): void { this.active.resume(); }
  dispose(): void {
    for (const off of this.offs) off();
    this.listeners.clear();
    this.own.dispose();
    this.second.dispose();
  }
}
