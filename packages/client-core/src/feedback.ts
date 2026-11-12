import {randomId} from './random-id.ts';
import {serverPath} from './query/server-api.ts';
import {installationIdentity} from './installation.ts';
import {serviceProblem, serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';

/** "Report a problem": one dialog's lifetime, sent to `POST /v1/reports`.
 *
 * A report carries the person's words and, as its context, what they picked it is about, the
 * title they were playing (its id and name) and that playback's id, the app, its version and
 * platform, and whether the device was online. Nothing else: not the device's own name, no token,
 * password, server address, file or folder path, and no log (the apps keep none to send). The owner reads it and can reply; the reply arrives in the
 * person's notifications.
 *
 * The message a person typed is never lost to a failed send: the dialog goes back to 'ready'
 * with the error shown and the draft intact (the caller owns it). One Idempotency-Key per draft,
 * so a send repeated after a lost answer is the same report, not a second. */

export type FeedbackApi = Readonly<{requestRaw(path: string, method: string, options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>): Promise<Readonly<{status: number; body?: unknown}>>}>;
export type FeedbackCategory = Readonly<{id: string; label: string; aboutPlayback: boolean}>;
export type FeedbackKind = Readonly<{id: string; label: string; categories: readonly FeedbackCategory[]}>;
export type FeedbackDraft = {kind: string; category: string; message: string; item?: Readonly<{id: string; title: string}>; playbackSessionId?: string};
export type FeedbackSnapshot = Readonly<{phase: 'ready' | 'sending' | 'sent'; error?: string}>;

/** The server takes up to 5,000 characters. */
export const FEEDBACK_MAX_MESSAGE = 5000;

const TAXONOMY: readonly (readonly [string, readonly string[]])[] = [
  ['playback', ['wont-start', 'buffering', 'stops-early', 'audio', 'subtitles', 'quality', 'other']],
  ['library', ['missing-item', 'wrong-match', 'wrong-artwork', 'wrong-details', 'duplicate', 'other']],
  ['apps', ['crash', 'layout', 'navigation', 'performance', 'other']],
  ['account', ['sign-in', 'profile', 'restrictions', 'other']],
  ['other', ['suggestion', 'other']],
];
/** What a report can be about, in the viewer's language. Playback's categories ask for the title
 * being played. */
export function feedbackKinds(): readonly FeedbackKind[] {
  return TAXONOMY.map(([kind, categories]) => Object.freeze({id: kind, label: serviceText(`feedback.kind.${kind}` as MessageId),
    categories: Object.freeze(categories.map(category => Object.freeze({id: category, aboutPlayback: kind === 'playback',
      label: serviceText((category === 'other' ? 'feedback.category.other' : `feedback.category.${kind}.${category}`) as MessageId)})))}));
}

const word = /^[A-Za-z0-9][A-Za-z0-9 ._+-]{0,39}$/;

export class FeedbackService {
  private state: FeedbackSnapshot = Object.freeze({phase: 'ready'});
  private readonly listeners = new Set<() => void>();
  private attempt = '';
  private attemptContext?: Record<string, unknown>;
  /** `online` reports whether the device itself is online, when the platform can tell.
   * `life` is the dialog's own: a send is refused before and its answer dropped after it ends. */
  constructor(private readonly api: FeedbackApi, private readonly operationId: () => string = () => randomId(), private readonly online?: () => boolean | undefined,
    private readonly life?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) {}
  private current() {return !this.life || !this.life.signal.aborted && this.life.isCurrent();}
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  private publish(next: FeedbackSnapshot) {this.state = Object.freeze(next); this.listeners.forEach(f => f());}

  /** Why a draft cannot be sent yet, or '' when it can. Lengths count characters as people do. */
  problem(draft: FeedbackDraft): string {
    const kind = feedbackKinds().find(k => k.id === draft.kind);
    if (!kind) return serviceText('feedback.chooseKind');
    if (!kind.categories.some(c => c.id === draft.category)) return serviceText('feedback.chooseCategory');
    const length = Array.from(draft.message.trim()).length;
    if (!length) return serviceText('feedback.empty');
    if (length > FEEDBACK_MAX_MESSAGE) return serviceText('feedback.tooLong', {count: FEEDBACK_MAX_MESSAGE});
    return '';
  }

  /** What the report says beside the person's words. */
  private context(draft: FeedbackDraft): Record<string, unknown> {
    const identity = installationIdentity(), online = this.online?.();
    const playback = feedbackKinds().find(k => k.id === draft.kind)?.categories.find(c => c.id === draft.category)?.aboutPlayback;
    return {about: `${draft.kind}/${draft.category}`,
      ...(draft.item ? {item: {id: draft.item.id, title: draft.item.title.slice(0, 300)}} : {}),
      ...(playback && draft.playbackSessionId ? {playbackSessionId: draft.playbackSessionId} : {}),
      // The app and its platform, never the device's own name (it is often a person's name).
      ...(identity ? {...(word.test(identity.platform) ? {platform: identity.platform} : {}),
        ...(word.test(identity.app) ? {app: identity.app} : {}), ...(word.test(identity.appVersion) ? {appVersion: identity.appVersion} : {})} : {}),
      ...(typeof online === 'boolean' ? {online} : {})};
  }

  async submit(draft: FeedbackDraft): Promise<boolean> {
    if (this.state.phase !== 'ready' || this.problem(draft) || !this.current()) return false;
    // The draft's facts are captured with its key, so a repeat is the same request.
    if (!this.attempt) {this.attempt = this.operationId(); this.attemptContext = this.context(draft);}
    this.publish({phase: 'sending'});
    try {
      const answer = await this.api.requestRaw(serverPath('POST /v1/reports'), 'POST', {headers: {'Idempotency-Key': this.attempt},
        body: {kind: 'problem', message: draft.message.trim(), context: this.attemptContext}, ...(this.life ? {signal: this.life.signal} : {})});
      if (!this.current()) return false;
      if (answer.status < 200 || answer.status >= 300) throw Object.assign(new Error('report refused'), {status: answer.status, ...(answer.body as {error?: object} | undefined)?.error});
      this.attempt = ''; this.attemptContext = undefined;
      this.publish({phase: 'sent'});
      return true;
    } catch (e) {
      if (!this.current()) return false;
      this.publish({phase: 'ready', error: serviceProblem(e, 'feedback', 'feedback.sendFailed', 'save')});
      return false;
    }
  }
}
