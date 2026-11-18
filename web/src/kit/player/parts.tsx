import type {ReactNode} from 'react';
import {Button, Slider} from '../controls';
import {Icon, type IconName} from '../Icon';
import styles from './parts.module.css';

/** The players' own parts: what a player needs beyond the stock buttons, rows and cards. The frames
 * (`kit/frames` `PlayerChromeFrame`, `AudioPlayerFrame`) place them; a player screen supplies their values and
 * handlers. The scrubber is `kit/media` `SeekLine`, with `PlayerScrubPreview` as its overlay while scrubbing. */

/** The title block at the top of the video player: the title, then the episode or the year under it. */
export function PlayerTitle({title, lead, subtitle}: {title: string; /** The part of the line under the title that is said first and brighter ("S1 E2"). */ lead?: string; subtitle?: string}) {
  return <div className={styles.title}><h2>{title}</h2>{(lead || subtitle) && <p>{lead && <b>{lead}</b>}{subtitle}</p>}</div>;
}

export type PlayerControlProps = Readonly<{
  icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean;
  /** Play or Pause: the large white one. */
  primary?: boolean;
  /** A top-bar button over the picture: on a glass disc from tablet width (desktop-player), a plain white glyph on a
   * phone (phone-player). */
  onPicture?: boolean;
  /** Skip back or forward: the seconds, drawn inside the arrow. */
  skip?: number;
  /** Buffering: the spinner takes the icon's place and the button keeps its size. */
  busy?: boolean;
}>;
/** One round transport or tool button: the kit Button at its `transport` size. A control the title doesn't support
 * stays in its place with `disabled`. */
export function PlayerControl({icon, label, onPress, disabled, active, primary = false, onPicture = false, skip, busy = false}: PlayerControlProps) {
  const button = <Button size="transport" variant={primary ? 'primary' : onPicture ? 'glass' : 'icon'} className={onPicture ? styles.onPicture : undefined} iconOnly icon={icon} aria-label={label} title={label} aria-pressed={active} disabled={disabled} loading={busy} onClick={onPress} />;
  return skip === undefined ? button : <span className={styles.skip}>{button}<small aria-hidden="true">{skip}</small></span>;
}

/** The time read-outs under a scrubber: where it is, and how long is left. (The video frame places its own.) */
export function PlayerTimes({position, remaining}: {position: string; remaining?: string}) {
  return <div className={styles.times}><b>{position}</b><b>{remaining}</b></div>;
}

/** Mute and the volume line beside it. */
export function PlayerVolume({value, muted, label, muteLabel, onChange, onMute}: {value: number; muted: boolean; label: string; muteLabel: string; onChange: (value: number) => void; onMute: () => void}) {
  return <span className={styles.volume}><PlayerControl icon="i-vol" label={muteLabel} active={muted} onPress={onMute} />
    <Slider min={0} max={1} step={.01} value={muted ? 0 : value} aria-label={label} onValueChange={onChange} /></span>;
}

/** An options entry point that says what it is and what it is set to: "Subtitles English", "Audio English", and
 * quality as "Auto 1080p". With `name` the name is the strong word and the value follows dimmer; without it the value
 * is the strong word and `qualifier` goes before it. One an item doesn't have keeps its place with `disabled`. */
export function PlayerPill({name, value, qualifier, icon, label, onPress, disabled}: {name?: string; value: string; /** Before the value, dimmer ("Auto"). */ qualifier?: string; icon?: IconName; /** For assistive technology and the tooltip. */ label: string; onPress: () => void; disabled?: boolean}) {
  return <button type="button" className={styles.pill} aria-label={label} title={label} disabled={disabled} onClick={onPress}>{icon && <Icon name={icon} />}
    {name !== undefined ? <>{name}<span>{value}</span></> : <>{qualifier && <span>{qualifier}</span>}{value}</>}</button>;
}

/** The frame at the pointer while scrubbing, as `SeekLine`'s `overlay`: the picture there and its time. */
export function PlayerScrubPreview({at, time, children}: {/** 0 to 1 along the scrubber. */ at: number; time: string; children: ReactNode}) {
  return <span className={styles.preview} aria-hidden="true" style={{insetInlineStart: `clamp(var(--frame-player-preview-width) / 2, ${at * 100}%, 100% - var(--frame-player-preview-width) / 2)`}}>{children}<b>{time}</b></span>;
}

/** Skip intro, Up next: the white prompt in the player's prompt corner, with its dismiss beside it from tablet width. */
export function PlayerPrompt({label, icon = 'i-next', onPress, dismiss}: {label: string; icon?: IconName; onPress: () => void; dismiss?: Readonly<{label: string; onPress: () => void}>}) {
  return <div className={styles.prompts}>
    {dismiss && <button type="button" className={styles.dismiss} aria-label={dismiss.label} onClick={dismiss.onPress}><Icon name="i-x" /></button>}
    <button type="button" className={styles.prompt} onClick={onPress}>{label}<Icon name={icon} /></button>
  </div>;
}
