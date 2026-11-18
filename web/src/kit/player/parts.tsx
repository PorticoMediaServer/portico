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
  /** Over the picture with nothing behind it (the top bar's round buttons): on glass. */
  glass?: boolean;
  /** Skip back or forward: the seconds, drawn inside the arrow. */
  skip?: number;
  /** Buffering: the spinner takes the icon's place and the button keeps its size. */
  busy?: boolean;
}>;
/** One round transport or tool button: the kit Button at its `transport` size. A control the title doesn't support
 * stays in its place with `disabled`. */
export function PlayerControl({icon, label, onPress, disabled, active, primary = false, glass = false, skip, busy = false}: PlayerControlProps) {
  const button = <Button size="transport" variant={primary ? 'primary' : glass ? 'glass' : 'icon'} iconOnly icon={icon} aria-label={label} title={label} aria-pressed={active} disabled={disabled} loading={busy} onClick={onPress} />;
  return skip === undefined ? button : <span className={styles.skip}>{button}<small aria-hidden="true">{skip}</small></span>;
}

/** The time read-outs under the scrubber: where it is, and on the right when it ends and how long is left. A live
 * programme says Live in place of the time left. */
export function PlayerTimes({position, remaining, endsAt, live}: {position: string; remaining?: string; endsAt?: string; live?: string}) {
  return <div className={styles.times}><b>{position}</b><span>{endsAt}{live ? <b className={styles.live}>{live}</b> : <b>{remaining}</b>}</span></div>;
}

/** Mute and the volume line beside it. */
export function PlayerVolume({value, muted, label, muteLabel, onChange, onMute}: {value: number; muted: boolean; label: string; muteLabel: string; onChange: (value: number) => void; onMute: () => void}) {
  return <span className={styles.volume}><PlayerControl icon="i-vol" label={muteLabel} active={muted} onPress={onMute} />
    <Slider min={0} max={1} step={.01} value={muted ? 0 : value} aria-label={label} onValueChange={onChange} /></span>;
}

/** An options entry point that shows its current value: quality ("Auto 1080p"), speed ("1×"). */
export function PlayerPill({value, qualifier, icon, label, onPress, disabled}: {value: string; /** Said before the value, dimmer ("Auto"). */ qualifier?: string; icon?: IconName; label: string; onPress: () => void; disabled?: boolean}) {
  return <button type="button" className={styles.pill} aria-label={label} title={label} disabled={disabled} onClick={onPress}>{icon && <Icon name={icon} size={16} />}{qualifier && <span>{qualifier}</span>}{value}</button>;
}

/** The frame at the pointer while scrubbing, as `SeekLine`'s `overlay`: the picture there and its time. */
export function PlayerScrubPreview({at, time, children}: {/** 0 to 1 along the scrubber. */ at: number; time: string; children: ReactNode}) {
  return <span className={styles.preview} aria-hidden="true" style={{insetInlineStart: `clamp(var(--frame-player-preview-width) / 2, ${at * 100}%, 100% - var(--frame-player-preview-width) / 2)`}}>{children}<b>{time}</b></span>;
}

/** Skip intro, Up next: the white prompt in the player's prompt corner, with its dismiss beside it from tablet width. */
export function PlayerPrompt({label, icon = 'i-next', onPress, dismiss}: {label: string; icon?: IconName; onPress: () => void; dismiss?: Readonly<{label: string; onPress: () => void}>}) {
  return <div className={styles.prompts}>
    {dismiss && <button type="button" className={styles.dismiss} aria-label={dismiss.label} onClick={dismiss.onPress}><Icon name="i-x" size={20} /></button>}
    <button type="button" className={styles.prompt} onClick={onPress}>{label}<Icon name={icon} size={20} /></button>
  </div>;
}
