import {DetailService, type DetailApi, type DetailProjection, type DetailScope, type PersonalIntent} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';

export type NativeTitleEpisodeSnapshot = Readonly<{
  entry?: ContentEntry;
  phase: 'idle' | 'loading' | 'ready' | 'error';
  data?: DetailProjection;
  error?: unknown;
}>;

export type NativeTitleEpisodeOptions = Readonly<{
  api: DetailApi;
  scope: DetailScope;
  requestId: () => Promise<string>;
  /** The retained title owner's authority and lifetime. */
  current: () => boolean;
  play: (entry: ContentEntry, target: NonNullable<ContentEntry['playback']>, startIntent: 'resume' | 'start-over') => Promise<void>;
}>;

/** One episode opened in place under its row (Justin, 18 Oct 2026). The
 * episode's own detail owns its credits, actions and neighbour links; closing
 * it retires the read and the service. */
export class NativeTitleEpisodeDetails {
  private readonly options: NativeTitleEpisodeOptions;
  private service?: DetailService;
  private off?: () => void;
  private entry?: ContentEntry;
  private snapshot: NativeTitleEpisodeSnapshot = Object.freeze({phase: 'idle'});
  private listeners = new Set<() => void>();
  private retired = false;

  constructor(options: NativeTitleEpisodeOptions) {this.options = options;}
  private alive() {return !this.retired && this.options.current();}
  private publish(next: NativeTitleEpisodeSnapshot) {
    if (this.retired) return;
    this.snapshot = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.retired) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  open = (entry: ContentEntry): void => {
    if (!this.alive() || !entry.libraryId) return;
    this.close();
    const service = new DetailService({api: this.options.api, scope: this.options.scope, requestId: this.options.requestId});
    this.service = service; this.entry = entry;
    this.publish({entry, phase: 'loading'});
    this.off = service.subscribe(() => {
      if (this.service !== service || !this.alive()) return;
      const state = service.getSnapshot();
      this.publish({entry, phase: state.phase === 'ready' ? 'ready' : state.phase === 'error' ? 'error' : 'loading',
        ...(state.data ? {data: state.data} : {}), ...(state.error ? {error: state.error} : {})});
    });
    void service.select({libraryId: entry.libraryId, itemId: entry.id}).catch(error => {
      if (this.service === service && this.alive()) this.publish({entry, phase: 'error', error});
    });
  };
  close = (): void => {
    this.off?.(); this.off = undefined;
    this.service?.dispose(); this.service = undefined;
    this.entry = undefined;
    this.publish({phase: 'idle'});
  };
  retry = (): void => {
    const service = this.service;
    if (!service || !this.alive()) return;
    this.publish({...this.snapshot, phase: 'loading'});
    void service.refresh().catch(error => {if (this.service === service && this.alive()) this.publish({...this.snapshot, phase: 'error', error});});
  };
  play = async (fromStart = false): Promise<void> => {
    const entry = this.entry, data = this.snapshot.data;
    if (!this.alive() || !entry) return;
    const action = data?.actions.find(value => value.enabled && value.playback && (fromStart ? value.id === 'start_over' : ['resume', 'play'].includes(value.id)));
    const target = (fromStart ? action?.playback : action?.playback ?? entry.playback);
    if (!target) return;
    await this.options.play(entry, target, fromStart ? 'start-over' : 'resume');
  };
  personal = async (intent: PersonalIntent): Promise<void> => {
    if (!this.alive() || !this.service || !this.snapshot.data) return;
    await this.service.mutate(intent);
  };
  dispose = (): void => {
    if (this.retired) return;
    this.retired = true;
    this.off?.(); this.off = undefined;
    this.service?.dispose(); this.service = undefined;
    this.entry = undefined;
    this.listeners.clear();
    this.snapshot = Object.freeze({phase: 'idle'});
  };
}
