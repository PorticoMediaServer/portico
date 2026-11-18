import type {RefObject} from 'react';
import type {I18n} from '@i18n';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {TrackChoice} from '../media';

/** What the player's own panels are given: the panel that is open, the form it is drawn in, and how to close or move
 * between panels. Moved here from the player screen with the panels themselves. */
export type PlayerPanel = 'options' | 'subtitles-audio' | 'subtitles' | 'audio' | 'quality' | 'speed' | 'episodes' | 'chapters' | 'queue' | 'play-on' | 'info' | 'file-info' | 'versions' | 'equalizer';
export type PlayerPanelContext = Readonly<{
  panel: PlayerPanel | null;
  snapshot: WebPlaybackRuntimeSnapshot;
  layout: 'desktop' | 'phone';
  /** A compact video window wider than it is tall uses the authored side sheet. */
  landscape?: boolean;
  phoneTop: number;
  /** Current parent-offered title panels and the existing guarded feedback action. */
  available?: readonly PlayerPanel[];
  reportProblem?: Readonly<{label: string; action: () => void | Promise<void>}>;
  close: () => void;
  /** Navigate the parent's open panel, e.g. a phone row to its own sub-panel. */
  show: (panel: PlayerPanel) => void;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
}>;

/** One choice of a quality or speed list. */
export type PlayerRadioChoice=Readonly<{id:string;label:string;detail?:string;selected:boolean;enabled:boolean}>;
export type PlayerOptionsModel=Readonly<{
 fence:QueuePlaybackFence;audio:readonly TrackChoice[];subtitles:readonly TrackChoice[];subtitleAdditions:readonly TrackChoice[];
 quality:readonly PlayerRadioChoice[];speed:readonly PlayerRadioChoice[];
 qualityLabel?:string;qualityAccessibleLabel?:string;qualityNotes:readonly string[];audioNotes:readonly string[];/** The one route line the retained panel binding still reads. */qualityNote?:string;automatic:boolean;audioLabel?:string;subtitleLabel?:string;
 subtitleTiming?:Readonly<{offsetSeconds:number;shift(deltaMs:number):Promise<void>}>;
 /** desktop-player f3: Search for subtitles (07 rules 65 to 68). `reason` when it cannot run here; otherwise results with
  * Download, each under the provider's credit. Choosing one downloads it and turns it on. */
 subtitleSearch?:Readonly<{reason?:string;busy:boolean;searched:boolean;error?:string;canRetry:boolean;results:readonly Readonly<{id:string;primary:string;secondary:string}>[];credit:string;search():Promise<void>;choose(id:string):Promise<void>}>;
 /** The style this playback draws with; a change lasts until it stops. */
 subtitleStyle?:Readonly<{size:SubtitlePreferences['size'];background:SubtitlePreferences['background'];set(patch:Readonly<{size?:SubtitlePreferences['size'];background?:SubtitlePreferences['background']}>):void}>;
 sleepTimer?:Readonly<{minutes:number;set(minutes:number):void}>;
 /** The image track's own mixed forced lines; absent when it has none. */
 subtitleForced?:Readonly<{only:boolean;set(on:boolean):void}>;
 isCurrent():boolean;selectAudio(id:string):Promise<void>;selectSubtitle(id:string):Promise<void>;
 selectQuality(id:string):Promise<void>;selectSpeed(id:string):void;
}>;

export type PhoneOptionRow=Readonly<{id:'subtitles'|'audio'|'quality'|'speed';label:string;value?:string;detail?:string}>;
/** The drawn phone row list: one row per real control, carrying its current choice only. */
export function phoneOptionRows(model:PlayerOptionsModel,t:I18n['t']):readonly PhoneOptionRow[]{
 return [
  ...(model.subtitles.length?[{id:'subtitles' as const,label:t('player.opt.subtitles'),value:model.subtitleLabel??t('player.opt.off')}]:[]),
  ...(model.audio.length?[{id:'audio' as const,label:t('player.opt.audio'),...(model.audioLabel?{value:model.audioLabel}:{})}]:[]),
  ...(model.quality.length?[{id:'quality' as const,label:t('player.opt.quality'),value:model.automatic?t('next.kit.quality.automatic'):model.qualityLabel,...(model.automatic&&model.qualityLabel?{detail:model.qualityLabel}:{})}]:[]),
  ...(model.speed.length?[{id:'speed' as const,label:t('player.opt.speed'),value:model.speed.find(row=>row.selected)?.label}]:[]),
 ];
}

/** The Episodes panel's rows: the open season of the title that is playing. */
export type PlayerEpisodeSeason = Readonly<{id: string | null; number: number; title: string}>;
export type PlayerEpisodeRow = Readonly<{
  itemId: string;
  title: string;
  number?: number;
  stillUrl?: string;
  overview?: string;
  airDate?: string;
  durationSeconds?: number;
  progressSeconds?: number;
  watched: boolean;
  playing: boolean;
  next: boolean;
}>;
export type PlayerEpisodeList = Readonly<{
  seasons: readonly PlayerEpisodeSeason[];
  seasonId: string | null;
  rows: readonly PlayerEpisodeRow[];
}>;
