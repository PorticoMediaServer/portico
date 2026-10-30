import {FeedbackService, type FeedbackDraft, type FeedbackSnapshot, type InboxApi} from '@core/inbox.ts';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';

export type FeedbackTarget = Readonly<{
  item: Readonly<{id: string; title: string}>;
  playback?: Readonly<{fence: QueuePlaybackFence; isCurrent: () => boolean; subscribe: (listener: () => void) => () => void}>;
}>;
export type FeedbackViewer = Readonly<{serverId: string; authority: string; accountId: string; profileId: string}>;
export type ViewerFeedbackSnapshot = Readonly<{
  open: boolean; canSubmit: boolean; service: FeedbackSnapshot; draft: FeedbackDraft; target?: FeedbackTarget;
}>;
const emptyDraft = (): FeedbackDraft => ({kind: '', category: '', message: '', attachDiagnostics: true});
const loading: FeedbackSnapshot = Object.freeze({phase: 'loading'});

/** One captured viewer's actual service. Imports/constructor do no I/O. Each
 * dialog has its own abort/idempotent draft lifetime; authority or source
 * retirement clears it before a late answer can return to the screen. */
export class ViewerFeedback {
  private state: ViewerFeedbackSnapshot = Object.freeze({open: false, canSubmit: false, service: loading, draft: emptyDraft()});
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private service?: FeedbackService;
  private offService?: () => void;
  private offPlayback?: () => void;
  private disposed = false;
  private epoch = 0;
  constructor(private readonly api: InboxApi, private readonly viewer: FeedbackViewer, private readonly isCurrent: () => boolean, private readonly operationId?: () => string) {}
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
  private read(open: boolean, target?: FeedbackTarget) {
    this.stop(); const epoch = this.epoch, controller = this.controller = new AbortController();
    const current = () => !this.disposed && this.isCurrent() && epoch === this.epoch && !controller.signal.aborted && this.targetCurrent(target);
    const api: InboxApi = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!current()) throw new DOMException('Feedback authority retired', 'AbortError');
      const answer = await this.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, controller.signal]) : controller.signal);
      if (!current()) throw new DOMException('Feedback authority retired', 'AbortError');
      return answer;
    }};
    const service = this.service = new FeedbackService(api, this.operationId, () => typeof navigator === 'undefined' ? undefined : navigator.onLine);
    this.publish({open, canSubmit: this.state.canSubmit, service: service.getSnapshot(), draft: emptyDraft(), target});
    this.offService = service.subscribe(() => {
      if (!current()) return;
      const next = service.getSnapshot(), capabilities = next.capabilities;
      let draft = this.state.draft;
      if (capabilities && !draft.kind) {
        const kind = (target?.playback ? capabilities.kinds.find(value => value.categories.some(category => category.wantsPlaybackSession)) : undefined) ?? capabilities.kinds[0];
        if (kind) draft = {...draft, kind: kind.id, category: kind.categories[0]?.id ?? ''};
      }
      this.publish({...this.state, service: next, draft, canSubmit: capabilities ? capabilities.canSubmit : next.phase === 'loading' && this.state.canSubmit});
    });
    if (target?.playback) {
      this.offPlayback = target.playback.subscribe(() => {if (!current()) this.close();});
      if (!current()) {this.close(); return;}
    }
    void service.open();
  }
  initialize() {if (!this.disposed && this.isCurrent() && !this.service) this.read(false);}
  open(target?: FeedbackTarget): boolean {
    if (!this.available() || !this.targetCurrent(target) || this.state.open) return false;
    this.read(true, target); return true;
  }
  close = () => {
    this.stop(); this.publish({open: false, canSubmit: this.state.canSubmit, service: loading, draft: emptyDraft()});
  };
  change(patch: Partial<Pick<FeedbackDraft, 'kind' | 'category' | 'message' | 'attachDiagnostics'>>) {
    if (!this.available() || !this.targetCurrent() || !this.state.open || this.state.service.phase !== 'ready') return;
    const capabilities = this.state.service.capabilities;
    if (!capabilities) return;
    let draft = {...this.state.draft};
    if (patch.kind !== undefined) {
      const kind = capabilities.kinds.find(value => value.id === patch.kind);
      if (!kind) return; draft = {...draft, kind: kind.id, category: kind.categories[0]?.id ?? ''};
    }
    const kind = capabilities.kinds.find(value => value.id === draft.kind);
    if (patch.category !== undefined) {if (!kind?.categories.some(value => value.id === patch.category)) return; draft.category = patch.category;}
    if (patch.message !== undefined) draft.message = patch.message;
    if (patch.attachDiagnostics !== undefined && capabilities.diagnosticsOptional) draft.attachDiagnostics = patch.attachDiagnostics;
    this.publish({...this.state, draft});
  }
  private complete(): FeedbackDraft {
    return {...this.state.draft, itemId: this.state.target?.item.id, playbackSessionId: this.state.target?.playback?.fence.sessionId};
  }
  problem = () => this.service?.problem(this.complete()) ?? '';
  async submit(): Promise<boolean> {
    if (!this.available() || !this.targetCurrent() || !this.state.open || this.state.service.phase !== 'ready' || !this.service) return false;
    return this.service.submit(this.complete());
  }
  dispose() {
    if (this.disposed) return;
    this.stop(); this.publish({open: false, canSubmit: false, service: loading, draft: emptyDraft()});
    this.disposed = true; this.listeners.clear();
  }
}
