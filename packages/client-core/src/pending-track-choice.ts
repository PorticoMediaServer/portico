import {sameLanguage} from './presentation/language.ts';
import {subtitleTrackNames} from './presentation/title-files.ts';
import type {MessageId, MessageValues} from '../../i18n/src/index.ts';

/**
 * Pick before play (Spec — Title Pages §1): the audio track and subtitles chosen on a title page,
 * held until the player's session can honour them, then applied once. One rule for every client:
 *
 * - Audio goes first. With an audio plan the rendition switches in place. Without one, naming a
 *   track asks for the title again (a new session).
 * - Subtitles are chosen on the session that is left standing — never on one an audio choice is
 *   about to replace. A track picked from the title's own list (playback options, one entry per
 *   track: unknown-language, SDH and forced tracks each their own) is named by its resource id and
 *   chosen exactly: if the session does not offer that very track nothing else is picked in its
 *   place. Only a choice made without a track list names a language, matched loosely since a file
 *   says "fra" where a subtitle plan says "fr".
 *
 * A choice never outlives one play.
 */
export type PendingTrackChoice = Readonly<{itemId: string; versionId?: string;
  /** Local-only original screen/authority witness; never serialized or rebound. */
  isCurrent?: () => boolean;
  audioStreamIndex?: number; subtitle?: 'off' | Readonly<{language?: string; resourceId?: string}>}>;

let pending: PendingTrackChoice | undefined;

export function setPendingTrackChoice(choice: PendingTrackChoice | undefined): void {
  pending = choice;
}

/** The choice for this item, consumed (cleared) on read. */
export function takePendingTrackChoice(itemId: string): PendingTrackChoice | undefined {
  if (!pending || pending.itemId !== itemId) return undefined;
  const out = pending;
  pending = undefined;
  return out.isCurrent?.() === false ? undefined : out;
}

export function peekPendingTrackChoice(itemId: string): PendingTrackChoice | undefined {
  return pending && pending.itemId === itemId ? pending : undefined;
}

/** What is still to apply, and the session an audio restart was asked from. */
export type PendingChoiceProgress = Readonly<{choice?: PendingTrackChoice; restart?: Readonly<{sessionId: string; stream: number}>}>;

/** The player's session as the rule needs to see it. */
export type PendingChoiceSession = Readonly<{
  sessionId: string;
  /** The audio plan: `loading` until the server has said whether it offers one. */
  audio: Readonly<{availability: string; renditions?: readonly Readonly<{id: string; sourceStreamIndex?: number; unavailable?: boolean}>[]}>;
  /** The file's own audio tracks, offered when there is no plan. */
  tracks?: Readonly<{tracks: readonly Readonly<{streamIndex: number}>[]; selected: number | null; pending?: number}>;
  /** The session's subtitle plan, once read. */
  subtitles?: Readonly<{mode: string; selectedId?: string; busy: boolean; resources: readonly Readonly<{id: string; language?: string; enabled: boolean}>[]}>;
}>;

export type PendingChoiceActions = Readonly<{
  selectRendition: (renditionId: string) => void;
  selectTrack: (streamIndex: number) => void;
  /** `null` turns subtitles off. */
  chooseSubtitle: (resourceId: string | null) => void;
}>;

/**
 * One step. Call it whenever the session, the audio state or the subtitle plan changes, and keep
 * what it returns; it does nothing once the choice is spent.
 */
export function advancePendingChoice(progress: PendingChoiceProgress, session: PendingChoiceSession, actions: PendingChoiceActions): PendingChoiceProgress {
  let {choice, restart} = progress;
  if (!choice) return progress;
  if (choice.audioStreamIndex !== undefined) {
    const wanted = choice.audioStreamIndex;
    if (session.audio.availability === 'loading') return progress;
    const rendition = session.audio.renditions?.find(r => r.sourceStreamIndex === wanted && !r.unavailable);
    if (rendition) actions.selectRendition(rendition.id);
    else if (session.audio.availability !== 'available') {
      const tracks = session.tracks;
      if (!tracks || tracks.pending !== undefined) return progress;
      if (tracks.selected !== wanted && tracks.tracks.some(t => t.streamIndex === wanted)) {
        restart = {sessionId: session.sessionId, stream: wanted};
        actions.selectTrack(wanted);
      }
    }
    choice = {...choice, audioStreamIndex: undefined};
  }
  const subtitle = choice.subtitle;
  if (subtitle !== undefined) {
    // The session the audio choice replaced is not the one to choose subtitles on.
    const restarting = session.tracks?.pending !== undefined || (restart?.sessionId === session.sessionId && session.tracks?.selected !== restart.stream);
    const plan = session.subtitles;
    if (!restarting && plan && !plan.busy) {
      if (subtitle === 'off') { if (plan.mode !== 'off') actions.chooseSubtitle(null); }
      else {
        const resource = plan.resources.find(r => r.enabled && (subtitle.resourceId ? r.id === subtitle.resourceId : sameLanguage(r.language, subtitle.language)));
        if (resource && plan.selectedId !== resource.id) actions.chooseSubtitle(resource.id);
      }
      choice = {...choice, subtitle: undefined};
    }
  }
  const spent = choice.audioStreamIndex === undefined && choice.subtitle === undefined;
  return {...(spent ? {} : {choice}), ...(restart ? {restart} : {})};
}

/** One pre-play subtitle entry: the track's resource id (what `subtitle.resourceId` names) and its menu name. */
export type PreplaySubtitle = Readonly<{id: string; label: string}>;

/**
 * The pre-play subtitle list for one version (`PlaybackOptions.versions[].subtitles`): every
 * track the server lists, in its order, each under its own name. Two tracks in one language stay
 * two entries, and a track with no language is listed by its title (or "Track n").
 */
export function preplaySubtitles(tracks: readonly Readonly<{id: string; language?: string; title?: string; format?: string; forced: boolean; sdh: boolean}>[], t: (id: MessageId, values?: MessageValues) => string, locale?: string): readonly PreplaySubtitle[] {
  const names = subtitleTrackNames(tracks.map(x => ({id: x.id, language: x.language, title: x.title, format: x.format, forced: x.forced, hearingImpaired: x.sdh})), t, locale);
  return Object.freeze(tracks.map(x => Object.freeze({id: x.id, label: names.get(x.id) ?? x.id})));
}
