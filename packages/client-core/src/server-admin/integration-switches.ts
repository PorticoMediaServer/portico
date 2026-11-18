/**
 * The owner's two Integrations switches (`GET`/`PUT /v1/admin/settings/integrations`): the Jellyfin-compatible routes
 * that companion tools such as Sonarr, Radarr and Home Assistant sign in to, and the assistant address. Both start off
 * (definition 13 rule 37); turning the compatible routes off also ends what was signed in through them.
 * The Integrations page draws them; this reads and saves, one change at a time, and keeps the server's answer.
 */
import type {IntegrationSettings} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

export type IntegrationSwitch = keyof IntegrationSettings;
export type IntegrationSwitchesSnapshot = Readonly<{
  phase: 'loading' | 'ready' | 'error';
  /** What the server keeps; a failed save leaves it as it was. */
  value?: IntegrationSettings;
  /** The switch being saved. */
  saving?: IntegrationSwitch;
  /** The last failed read or save. Present it through the app's error words. */
  error?: unknown;
}>;

export class IntegrationSwitchesSource {
  private state: IntegrationSwitchesSnapshot = Object.freeze({phase: 'loading'});
  private readonly listeners = new Set<() => void>();
  private readonly life = new AbortController();
  constructor(private readonly api: ServerRequester, private readonly isCurrent: () => boolean) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private live = () => !this.life.signal.aborted && this.isCurrent();
  private publish(patch: Partial<IntegrationSwitchesSnapshot>) {
    if (!this.live()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  /** The first read, and Try again. */
  load = async (): Promise<void> => {
    if (!this.live()) return;
    this.publish({phase: 'loading', error: undefined});
    try {
      const value = await callServer(this.api, 'GET /v1/admin/settings/integrations', {signal: this.life.signal});
      this.publish({phase: 'ready', value: Object.freeze({assistants: value.assistants === true, compatibleRoutes: value.compatibleRoutes === true})});
    } catch (error) {this.publish({phase: 'error', error});}
  };
  /** Turns one switch on or off; true when the server kept it. A save while another is pending is refused. */
  set = async (name: IntegrationSwitch, on: boolean): Promise<boolean> => {
    if (!this.live() || this.state.phase !== 'ready' || this.state.saving) return false;
    this.publish({saving: name, error: undefined});
    try {
      const value = await callServer(this.api, 'PUT /v1/admin/settings/integrations', {body: {[name]: on}, signal: this.life.signal});
      this.publish({saving: undefined, value: Object.freeze({assistants: value.assistants === true, compatibleRoutes: value.compatibleRoutes === true})});
      return this.live() && this.state.value?.[name] === on;
    } catch (error) {this.publish({saving: undefined, error}); return false;}
  };
  dispose() {this.life.abort(); this.listeners.clear();}
}
