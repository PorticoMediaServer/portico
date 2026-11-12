import {FeedbackService, feedbackKinds, type FeedbackApi, type FeedbackDraft, type FeedbackSnapshot} from '@core/feedback.ts';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';

export type FeedbackTarget = Readonly<{
  item: Readonly<{id: string; title: string}>;
  playback?: Readonly<{fence: QueuePlaybackFence; isCurrent: () => boolean; subscribe: (listener: () => void) => () => void}>;
}>;
export type FeedbackViewer = Readonly<{serverId: string; authority: string; accountId: string; profileId: string}>;
export type ViewerFeedbackSnapshot = Readonly<{
  open: boolean; canSubmit: boolean; service: FeedbackSnapshot; draft: FeedbackDraft; target?: FeedbackTarget;
}>;
const emptyDraft = (): FeedbackDraft => ({kind: '', category: '', message: ''});
const ready: FeedbackSnapshot = Object.freeze({phase: 'ready'});

/** One captured viewer's "Report a problem". Imports and the constructor do no I/O. Each dialog
 * has its own lifetime and report key; authority or source retirement closes it before a late
 * answer can return to the screen. */
export class ViewerFeedback {
  private state: ViewerFeedbackSnapshot = Object.freeze({open: false, canSubmit: false, service: ready, draft: emptyDraft()});
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private service?: FeedbackService;
  private offService?: () => void;
  private offPlayback?: () => void;
  private disposed = false;
  private epoch = 0;
  constructor(private readonly api: FeedbackApi, private readonly viewer: FeedbackViewer, private readonly isCurrent: () => boolean, private readonly operationId?: () => string) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  available = () => !this.disposed && this.isCurrent() && this.state.canSubmit;
  private targetCurrent(target = this.state.target) {
    if (!target?.playback) return true;
    const {fence, isCurrent} = target.playback, scope = fence.playback.scope;
    return fence.kind === 'queue' && fence.itemId === target.item.id && !!fence.sessionId && isCurrent() &&
      (['serverId', 'authority', 'accountId', 'profileId'] as const).every(key => scope[key] === this.viewer[key]);
  }
  private publish(next: ViewerFeedbackSnapshot) {
    if (this.disposed) return;
    this.state = Object.freeze(next);
    for (const listener of this.listeners) listener();
  }
  private stop() {
    ++this.epoch; this.controller?.abort(); this.controller = undefined;
    this.offService?.(); this.offService = undefined; this.service = undefined;
    this.offPlayback?.(); this.offPlayback = undefined;
  }
  private read(target?: FeedbackTarget) {
    this.stop(); const epoch = this.epoch, controller = this.controller = new AbortController();
    const current = () => !this.disposed && this.isCurrent() && epoch === this.epoch && !controller.signal.aborted && this.targetCurrent(target);
    const service = this.service = new FeedbackService(this.api, this.operationId, () => typeof navigator === 'undefined' ? undefined : navigator.onLine,
      {signal: controller.signal, isCurrent: current});
    // Opened from the player, the report starts as a playback one.
    const kind = feedbackKinds().find(value => value.id === (target?.playback ? 'playback' : '')) ?? feedbackKinds()[0]!;
    this.publish({open: true, canSubmit: true, service: service.getSnapshot(), draft: {...emptyDraft(), kind: kind.id, category: kind.categories[0]!.id}, target});
    this.offService = service.subscribe(() => {if (current()) this.publish({...this.state, service: service.getSnapshot()});});
    if (target?.playback) {
      this.offPlayback = target.playback.subscribe(() => {if (!current()) this.close();});
      if (!current()) this.close();
    }
  }
  /** Anyone signed in can report a problem; there is nothing to load first. */
  initialize() {if (!this.disposed && this.isCurrent() && !this.state.canSubmit) this.publish({...this.state, canSubmit: true});}
  open(target?: FeedbackTarget): boolean {
    if (!this.available() || !this.targetCurrent(target) || this.state.open) return false;
    this.read(target); return true;
  }
  close = () => {
    this.stop(); this.publish({open: false, canSubmit: this.state.canSubmit, service: ready, draft: emptyDraft()});
  };
  change(patch: Partial<Pick<FeedbackDraft, 'kind' | 'category' | 'message'>>) {
    if (!this.available() || !this.targetCurrent() || !this.state.open || this.state.service.phase !== 'ready') return;
    let draft = {...this.state.draft};
    if (patch.kind !== undefined) {
      const kind = feedbackKinds().find(value => value.id === patch.kind);
      if (!kind) return; draft = {...draft, kind: kind.id, category: kind.categories[0]!.id};
    }
    const kind = feedbackKinds().find(value => value.id === draft.kind);
    if (patch.category !== undefined) {if (!kind?.categories.some(value => value.id === patch.category)) return; draft.category = patch.category;}
    if (patch.message !== undefined) draft.message = patch.message;
    this.publish({...this.state, draft});
  }
  /** The title and its playback travel with the report only when the dialog was opened on them. */
  private complete(): FeedbackDraft {
    const target = this.state.target;
    return {...this.state.draft, ...(target ? {item: target.item} : {}), ...(target?.playback ? {playbackSessionId: target.playback.fence.sessionId} : {})};
  }
  problem = () => this.service?.problem(this.complete()) ?? '';
  async submit(): Promise<boolean> {
    if (!this.available() || !this.targetCurrent() || !this.state.open || this.state.service.phase !== 'ready' || !this.service) return false;
    return this.service.submit(this.complete());
  }
  dispose() {
    if (this.disposed) return;
    this.stop(); this.publish({open: false, canSubmit: false, service: ready, draft: emptyDraft()});
    this.disposed = true; this.listeners.clear();
  }
}
