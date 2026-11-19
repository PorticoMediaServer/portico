import {useSyncExternalStore, type ReactNode} from 'react';
import type {ArtworkSource} from '../artwork';
import {Button} from '../controls';
import {Icon, type IconName} from '../Icon';
import {PlayerControl} from '../player';
import {AudioChoiceRow} from '../player/Queue';
import {Skeleton} from '../states';
import {ArtBox} from './parts';
import styles from './player-frames.module.css';

/** Frames 7 and 8, the two players, with the native frames' names and the regions `frameRegions.player` and
 * `frameRegions.audio` name (packages/design). Neither draws a backdrop: the video player's layer is black and the
 * audio player's layer is the glow from its artwork (R10). FRAMES.md is the brief. */

/** The time read-outs; the frame places them for the form (under the scrubber, either side of it, or as one clock). */
export type PlayerReadouts = Readonly<{position: string; /** With a minus sign: the time left. */ remaining?: string; duration?: string; /** "Ends 4:48 PM". */ endsAt?: string; /** Said in place of the time left. */ live?: string}>;
/** The video player's three web forms: an upright phone (the 16:9 strip and the watch page under it), a phone on its side
 * (the transport in the middle of the picture), and the wide form (desktop-player, tablet-play). */
export type PlayerForm = 'upright' | 'sideways' | 'wide';
const formQueries = {upright: '(max-width: 599px)', sideways: '(max-height: 500px) and (min-width: 600px)'} as const;
const subscribeForm = (changed: () => void) => {
  const lists = Object.values(formQueries).map(query => window.matchMedia(query));
  lists.forEach(list => list.addEventListener('change', changed));
  return () => lists.forEach(list => list.removeEventListener('change', changed));
};
const windowForm = (): PlayerForm => window.matchMedia(formQueries.upright).matches ? 'upright' : window.matchMedia(formQueries.sideways).matches ? 'sideways' : 'wide';

export type PlayerChromeFrameProps = Readonly<{
  /** The video surface, or the frozen frame when playback failed. */
  picture: ReactNode;
  /** Whether the controls show; the player decides when they hide. */
  chromeVisible: boolean;
  /** Close (a `PlayerControl onPicture`), top left. */
  close: ReactNode;
  /** `PlayerTitle`, beside Close. Not drawn on an upright phone, where the watch page under the picture has the title. */
  title?: ReactNode;
  /** The buttons opposite Close (`PlayerControl onPicture`): Play on, picture in picture, settings. */
  tools?: ReactNode;
  /** The scrubber (`SeekLine`). */
  scrubber: ReactNode;
  times: PlayerReadouts;
  /** Previous, back, Play or Pause, forward, next (`PlayerControl`s, at the transport's icon classes). In the middle of
   * the bottom bar from tablet width; in the middle of the picture on a phone, upright or on its side. */
  transport: ReactNode;
  /** The bar's left end (Episodes) and right end (the options: `PlayerPill`s, volume, full screen). */
  leading?: ReactNode; trailing?: ReactNode;
  /** `PlayerPrompt`: Skip intro, Skip recap, the automatic-skip note with Undo. The prompt corner, bottom right above
   * the bar; it stays when the chrome hides. */
  prompts?: ReactNode;
  /** `PlayerUpNext` over the credits, or Still watching?: alone in the picture's bottom right corner, the controls
   * hidden (desktop-player 4, 4s, phone-player 6, 6e). The end card after a film (`PlayerEndCard`) fills the picture. */
  card?: ReactNode;
  /** One of the player's own panels, open (`PlayerOptions`, `PlayerEpisodes`). `panelCovers` says what it lies over,
   * which hides: `bar` for the Episodes drawer (desktop-player 6), `all` on a phone on its side (phone-player 3). */
  panel?: ReactNode; panelCovers?: 'bar' | 'all';
  /** Paused under a panel that covers the bar (the Episodes drawer): the resume disc in the middle of the picture the
   * panel leaves (desktop-player f6). The screen passes it only while paused. */
  resume?: ReactNode;
  /** A phone: the finger is on the scrubber, so the transport in the middle of the picture makes way for the preview. */
  scrubbing?: boolean;
  /** The form, when the screen chooses it (a phone's Full screen makes an upright phone the full-window player); by default
   * the window's: under 600 wide upright, 500 or less high sideways, else wide. */
  form?: PlayerForm;
  /** An upright phone only: the watch page under the picture (the title page's own content). */
  body?: ReactNode;
}>;
/** Frame 7, the video player's chrome, in its three drawn forms. From tablet width (desktop-player, tablet-play): the
 * picture fills the window; top bar, then a bar of scrubber, read-outs and a row of left end, transport, right end.
 * A phone on its side (phone-player 1 and 2; R8: any window this short, whatever its width): the same, with the
 * transport in the middle of the picture and the read-outs either side of the scrubber. An upright phone (phone-player
 * 4): the picture is a 16:9 strip with the chrome inside it, the scrubber on its lower edge, and the watch page below.
 * Every region is fixed; the parts that fill it are `kit/player`'s. */
export function PlayerChromeFrame({picture, chromeVisible, close, title, tools, scrubber, times, transport, leading, trailing, prompts, card, panel, panelCovers, resume, scrubbing, body, form}: PlayerChromeFrameProps) {
  const windowShape = useSyncExternalStore(subscribeForm, windowForm, () => 'wide' as const), drawn = form ?? windowShape;
  const covered = card ? 'all' : panel ? panelCovers : undefined, shown = chromeVisible && covered !== 'all';
  const right = times.live ? <b className={styles.playerLive}>{times.live}</b> : <b>{times.remaining}</b>;
  return <div className={styles.player} data-form={drawn} data-chrome={shown} data-covered={covered} data-scrubbing={scrubbing || undefined}>
    <div className={styles.playerStage}>
      <div className={styles.playerPicture}>{picture}</div>
      <div className={styles.playerShade} />
      <div className={styles.playerTop} inert={!shown}>{close}<div className={styles.playerTitle}>{title}</div><div className={styles.playerTools}>{tools}</div></div>
      <div className={styles.playerBottom}>
        <div className={styles.playerPrompts}>{!card && !panel && prompts}</div>
        <div className={styles.playerBar} inert={!shown || covered === 'bar'}>
          <div className={styles.playerSeek}>{scrubber}</div>
          <span className={styles.playerPosition}><b>{times.position}</b></span>
          <span className={styles.playerRemaining}><span className={styles.playerEnds}>{times.endsAt}</span>{right}</span>
          <span className={styles.playerClock}>{times.live ? <b className={styles.playerLive}>{times.live}</b> : <><b>{times.position}</b>{times.duration !== undefined && ` / ${times.duration}`}</>}</span>
          <div className={styles.playerLeading}>{leading}</div>
          <div className={styles.playerCentre}>{transport}</div>
          <div className={styles.playerTrailing}>{trailing}</div>
        </div>
      </div>
      {covered === 'bar' && resume !== undefined && <div className={styles.playerResume}>{resume}</div>}
      {card}{panel}
    </div>
    {body !== undefined && <div className={styles.playerBody}>{body}</div>}
  </div>;
}

/** One control in a fixed place. A control the item doesn't support keeps its place with `disabled`, dimmed. */
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; /** Back or forward: the seconds it skips, drawn in the arrow. */ skip?: number}>;
/** An audiobook's Speed, Sleep timer and Chapters: a tile at phone width (its `value` over its label), and from 600
 * Speed and Sleep timer as a row of their `choices` with `help`. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string; help?: string; choices?: readonly Readonly<{label: string; selected?: boolean; onPress: () => void}>[]}>;
/** Where it is and, at the other end, the length (music from 600) or the time left; a phone's audiobook says the time left in the book between them. */
export type AudioTimes = Readonly<{position: string; end: string; middle?: string}>;
export type AudioPlayerFrameProps = Readonly<{
  close: AudioControl; context?: string; source?: string;
  /** "This browser": the bottom row's middle at phone width, the top right beside More from 600. */
  device?: AudioControl;
  more: AudioControl;
  /** A note over the top bar ("Went back 30 s, since it's been a while."), from 600. */
  notice?: string;
  artwork?: ArtworkSource | null; icon: IconName;
  title: string;
  /** "Unabridged", after the title, from 600. */
  badge?: string;
  /** The artist then the album; an audiobook's author then "Read by" the narrator. Phone width says the first only. */
  byline?: readonly string[];
  /** The heart at the end of the title row, and from 600 More beside it. */
  favourite?: AudioControl; titleMore?: AudioControl;
  chapter?: Readonly<{label: string; of: string}>;
  /** The scrubber line; the frame places `times` under its ends at phone width and at its ends from 600. */
  scrubber: ReactNode; times: AudioTimes;
  transport: readonly [AudioControl, AudioControl, AudioControl, AudioControl, AudioControl];
  /** `PlayerVolume`, under the transport, from 600. */
  volume?: ReactNode;
  /** Phone width only: what plays next. Its line is kept, blank, when there is nothing to say. */
  next?: Readonly<{label: string; title: string}>;
  /** Phone width only: the bottom row's Lyrics and Up next (from 600 they are the panel's tabs). */
  lyrics?: AudioControl; queue?: AudioControl;
  speed?: AudioOption; sleep?: AudioOption; chapters?: AudioOption;
  /** The queue panel (`AudioPanel`), the one flexible region: beside the column from 600, a sheet over the player at phone width while it is open. */
  panel?: ReactNode;
  loading?: boolean;
}>;
/** Frame 8, now playing (rule 12; R7: each form its own layout). Phone width (phone-app 14 and 15, final3-mobileweb-03
 * frame 8): one column with the bottom row. From 600 (desktop-listen "now" and "book-player"): the bar with Close, the
 * context, the device and More; a column as wide as the cover with the title, the heart and More, the scrubber with its
 * times at its ends, a compact transport and the volume; and the raised queue panel beside it. Every region keeps its box. */
export function AudioPlayerFrame({close, context, source, device, more, notice, artwork, icon, title, badge, byline = [], favourite, titleMore, chapter, scrubber, times, transport, volume, next, lyrics, queue, speed, sleep, chapters, panel, loading = false}: AudioPlayerFrameProps) {
  const book = chapter !== undefined, glyph = (item: AudioControl, className?: string) => <Button key={item.label} variant="icon" size="small" icon={item.icon} className={className} aria-label={item.label} aria-pressed={item.active} disabled={item.disabled || loading} onClick={item.onPress} />;
  const slot = (item: AudioControl | AudioOption, kind: 'slot' | 'device' | 'tile') => <Button key={item.label} variant="text" className={styles.audioSlot} data-kind={kind} aria-pressed={item.active} disabled={item.disabled || loading} onClick={item.onPress}>
    {'value' in item && item.value !== undefined ? <b>{item.icon && <Icon name={item.icon} />}{item.value}</b> : item.icon && <Icon name={item.icon} />}<small>{item.label}</small></Button>;
  const rows = [speed, sleep].filter((item): item is AudioOption => !!item?.choices);
  return <div className={styles.audio} data-book={book || undefined} data-panel={panel !== undefined || undefined}>
    <header className={styles.audioTop}>
      <Button variant="glass" size="small" iconOnly icon={close.icon} className={styles.audioSquare} aria-label={close.label} onClick={close.onPress} />
      <p className={styles.audioContext}><span>{loading ? '' : context}</span>{source !== undefined && !loading && <b>{source}</b>}</p>
      {notice !== undefined && !loading && <span className={styles.audioNotice} role="status"><Icon name="i-rew" />{notice}</span>}
      {device && <Button variant="glass" size="small" icon={device.icon} className={styles.audioDevice} disabled={device.disabled || loading} onClick={device.onPress}>{device.label}</Button>}
      <Button variant="glass" size="small" iconOnly icon={more.icon} className={styles.audioSquare} aria-label={more.label} disabled={more.disabled || loading} onClick={more.onPress} />
    </header>
    <div className={styles.audioColumn}>
      <div className={styles.audioArt}>{loading ? <Skeleton width="100%" height="100%" sheen /> : <ArtBox kind="square" source={artwork} icon={icon} alt={title} />}</div>
      <div className={styles.audioWords}>
        <div>{loading ? <><Skeleton line width="64%" /><Skeleton line small width="40%" /></> : <>
          {/* The title takes the lines it needs: it is never cut. */}
          <h1>{title}{badge !== undefined && <span className={styles.audioBadge}>{badge}</span>}</h1>
          <p>{byline.map((part, index) => <span key={part} data-rest={index > 0 || undefined}>{part}</span>)}</p>
        </>}</div>
        {!loading && !book && <span className={styles.audioMarks}>{favourite && glyph(favourite, styles.audioMark)}{titleMore && glyph(titleMore, `${styles.audioMark} ${styles.audioTitleMore}`)}</span>}
      </div>
      {chapter && <p className={styles.audioChapter}><Icon name="i-list" /><b>{loading ? '' : chapter.label}</b><span>{loading ? '' : chapter.of}</span></p>}
      <div className={styles.audioSeek}><span>{loading ? '' : times.position}</span><div>{scrubber}</div>{times.middle !== undefined && <span className={styles.audioMiddle}>{loading ? '' : times.middle}</span>}<span>{loading ? '' : times.end}</span></div>
      <div className={styles.audioTransport}>{transport.map((item, index) => <PlayerControl key={item.label} audio icon={item.icon} label={item.label} primary={index === 2} skip={item.skip} active={item.active}
        tone={!book && (index === 0 || index === 4) ? 'secondary' : undefined} disabled={item.disabled || loading} onPress={item.onPress} />)}</div>
      {rows.length > 0 && <div className={styles.audioRows}>{rows.map(item => <AudioChoiceRow key={item.label} label={item.label} help={item.help} choices={item.choices!} />)}</div>}
      {volume !== undefined && <div className={styles.audioVolume}>{volume}</div>}
      <p className={styles.audioNext}>{next && !loading && <>{next.label}  <b>{next.title}</b></>}</p>
      <div className={styles.audioOptions} data-tiles={book || undefined}>{book ? [speed, sleep, chapters, device].filter((item): item is AudioOption => item !== undefined).map(item => slot(item, 'tile'))
        : <>{lyrics && slot(lyrics, 'slot')}{device && slot(device, 'device')}{queue && slot(queue, 'slot')}</>}</div>
    </div>
    {panel !== undefined && <aside className={styles.audioPanel}>{panel}</aside>}
  </div>;
}
