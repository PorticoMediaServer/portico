import {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {RadioGroup} from 'radix-ui';
import type {DetailProjection} from '@core/detail.ts';
import type {AudioStream, MediaVersion, PlaybackOptions} from '@core/playback-v1/types.ts';
import {audioStreamIndex} from '@core/playback-v1/choices.ts';
import {channelsLabel, codecLabel} from '@core/presentation/title-files.ts';
import {peekPendingTrackChoice, preplaySubtitles, setPendingTrackChoice, type PendingTrackChoice} from '@core/pending-track-choice.ts';
import {BottomSheet} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import type {DetailBindings} from './bindings';
import {locale, t} from './text';
import styles from './detail.module.css';

/** The viewer's own edits to the row: the audio track's stream index and a subtitle resource id. */
type Chosen = Readonly<{audio?: number; subtitle?: string}>;

/** phone-detail frames 1, 2, 3, 5 and 6: one quiet row naming the audio track and subtitle mode of
 * the version the server would play, opening one sheet with that version's real tracks. The
 * choice is left for the player through the shared pending-choice rule; nothing here starts
 * playback, and a title with no real choice draws no row. */
export function PreplayChoices({binding, data}: {binding: DetailBindings; data: DetailProjection}) {
  const [read, setRead] = useState<Readonly<{data: DetailProjection; options: PlaybackOptions}>>();
  const [chosen, setChosen] = useState<Chosen>({}), [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const itemId = data.item.id;
  // The original currentness witness: this page's captured data, never a later projection.
  const isCurrent = useCallback(() => binding.context.isCurrent() && binding.service.getSnapshot().phase === 'ready' && binding.service.getSnapshot().data === data, [binding, data]);
  useEffect(() => {
    const request = new AbortController();
    setRead(undefined); setChosen({}); setOpen(false);
    // A malformed or failed read on a playable title is reported, never drawn as "no row".
    void binding.readVersions(data, request.signal).then(options => {
      if (!request.signal.aborted && isCurrent()) setRead({data, options});
    }, error => {
      if (!request.signal.aborted && isCurrent()) binding.context.effects.reportError(error);
    });
    return () => request.abort();
  }, [binding, data, isCurrent]);
  const options = read?.data === data ? read.options : undefined;
  // Only the version the server says it would play: the row names tracks for that version alone.
  const version: MediaVersion | undefined = options?.versions.find(value => value.id === options.preferred?.versionId);
  const audios = version?.audio ?? [], subtitles = version?.subtitles ?? [];
  const language = (code: string) => { try { return new Intl.DisplayNames(locale(), {type: 'language'}).of(code) ?? code; } catch { return code; } };
  const base = (track: AudioStream, index: number) => track.language && track.language !== 'und' ? language(track.language) : track.title ?? t('mediaInfo.track', {number: index + 1});
  const audioName = (track: AudioStream, index: number) => [base(track, index), channelsLabel({channels: track.channels, channelLayout: track.layout}, t)].filter(Boolean).join(' ');
  // Two tracks in one language and layout are told apart by their codec, never by their order.
  const audioDetail = (track: AudioStream, index: number) => [base(track, index), codecLabel(track.codec), channelsLabel({channels: track.channels, channelLayout: track.layout}, t)].filter(Boolean);
  const preferred = audios.find(track => track.id === options?.preferred?.audioId) ?? audios.find(track => track.default) ?? audios[0];
  const defaultIndex = preferred ? audioStreamIndex(preferred.id) : undefined;
  const current = audios.find(track => audioStreamIndex(track.id) === (chosen.audio ?? defaultIndex)) ?? preferred;
  const names = version ? preplaySubtitles(subtitles.map(track => ({id: track.id, language: track.language, title: track.title, format: track.format, forced: track.forced, sdh: track.sdh})), t, locale()) : [];
  const chosenAudio = chosen.audio !== undefined && chosen.audio !== defaultIndex ? chosen.audio : undefined;
  const chosenSubtitle = chosen.subtitle === undefined || chosen.subtitle === '' ? undefined : chosen.subtitle;
  const subtitleValue = chosenSubtitle === undefined ? t('title.subtitlesAutomatic')
    : chosenSubtitle === 'off' ? t('title.subtitlesOff') : names.find(name => name.id === chosenSubtitle)?.label ?? t('title.subtitlesAutomatic');
  // The shared rule's own object: the offered version and this page's currentness travel with it,
  // and a subtitle is named by its resource id so a real track list is never matched by language.
  // Committed before paint, so an immediate Play reads the choice the viewer just made.
  useLayoutEffect(() => {
    if (!version || !isCurrent()) return;
    const subtitle = chosenSubtitle === undefined ? undefined : chosenSubtitle === 'off' ? 'off' as const : {resourceId: chosenSubtitle};
    const choice: PendingTrackChoice | undefined = chosenAudio === undefined && subtitle === undefined ? undefined
      : {itemId, versionId: version.id, isCurrent, ...(chosenAudio === undefined ? {} : {audioStreamIndex: chosenAudio}), ...(subtitle === undefined ? {} : {subtitle})};
    if (choice) setPendingTrackChoice(choice);
    // Leaving the page, or listing another version, takes exactly this page's own choice with it.
    return () => { if (choice && peekPendingTrackChoice(itemId) === choice) setPendingTrackChoice(undefined); };
  }, [isCurrent, itemId, version, chosenAudio, chosenSubtitle]);
  if (!version || !current || (audios.length < 2 && subtitles.length === 0)) return null;
  const live = () => { if (isCurrent()) return true; setOpen(false); return false; };
  const pick = (next: Chosen) => { if (live()) setChosen(next); };
  return <>
    <button ref={trigger} type="button" className={styles.preplay} aria-haspopup="dialog" aria-label={`${t('title.audio')}: ${audioName(current, audios.indexOf(current))}, ${t('title.subtitles')}: ${subtitleValue}`} onClick={() => { if (live()) setOpen(true); }}>
      <span><Icon name="i-audio" size={16} />{audioName(current, audios.indexOf(current))}</span>
      <span><Icon name="i-cc" size={16} />{subtitleValue}</span>
      <Icon name="i-updown" size={16} />
    </button>
    <BottomSheet open={open} onOpenChange={value => { if (!value) setOpen(false); }} title={t('title.playbackChoices')} returnFocusRef={trigger} fitContent>
      <div className={styles.preplaySheet}>
        {audios.length > 1 && <RadioGroup.Root className={styles.preplayGroup} orientation="vertical" aria-label={t('title.audio')} value={current.id}
          onValueChange={id => {const track = audios.find(value => value.id === id);if (track) pick({audio: audioStreamIndex(track.id), subtitle: chosen.subtitle});}}>
          <h3>{t('title.audio')}</h3>
          {audios.map((track, index) => <RadioGroup.Item key={track.id} value={track.id} className={styles.preplayRow}>
            <span><span className={styles.preplayFacts}>{audioDetail(track, index).map((fact, part) => <span key={part}>{fact}</span>)}</span>{track.commentary && !/\bcommentary\b/i.test(track.title ?? '') && <small>{t('next.kit.commentary')}</small>}</span><Icon name="i-check" size={20} />
          </RadioGroup.Item>)}
        </RadioGroup.Root>}
        {subtitles.length > 0 && <RadioGroup.Root className={styles.preplayGroup} orientation="vertical" aria-label={t('title.subtitles')} value={chosenSubtitle === undefined ? 'auto' : chosenSubtitle === 'off' ? 'off' : `track:${chosenSubtitle}`}
          onValueChange={value => {const id = value.startsWith('track:') ? value.slice(6) : undefined;if (value === 'auto' || value === 'off' || names.some(name => name.id === id)) pick({audio: chosen.audio, subtitle: value === 'auto' ? '' : value === 'off' ? 'off' : id});}}>
          <h3>{t('title.subtitles')}</h3>
          <RadioGroup.Item value="auto" className={styles.preplayRow}><span>{t('title.subtitlesAutomatic')}</span><Icon name="i-check" size={20} /></RadioGroup.Item>
          <RadioGroup.Item value="off" className={styles.preplayRow}><span>{t('title.subtitlesOff')}</span><Icon name="i-check" size={20} /></RadioGroup.Item>
          {names.map(name => <RadioGroup.Item key={name.id} value={`track:${name.id}`} className={styles.preplayRow}><span>{name.label}</span><Icon name="i-check" size={20} /></RadioGroup.Item>)}
        </RadioGroup.Root>}
      </div>
    </BottomSheet>
  </>;
}
