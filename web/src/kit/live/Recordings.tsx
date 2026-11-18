import type {ReactNode} from 'react';
import {Artwork, ChannelMark, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {Flag, type GuideChannel, type GuideFlag} from './Guide';
import styles from './live.module.css';

export type RecordingRowProps = Readonly<{
  time: string; range?: string; title: string; flags?: readonly GuideFlag[]; detail?: string;
  channel?: Omit<GuideChannel, 'cells'>; source?: string; artwork?: ArtworkSource;
  status?: Readonly<{text: string; tone?: 'record' | 'accent'}>;
  onPress?: () => void; more?: Readonly<{label: string; onPress: () => void}>;
}>;
/** One upcoming or recording-now row (desktop-live live-upcoming, phone-app 12): the time, the picture from tablet width,
 * the title, its flags and episode, the channel and the rule, the status, and More. */
export function RecordingRow({time, range, title, flags, detail, channel, source, artwork, status, onPress, more}: RecordingRowProps) {
  const statusText = status && `${status.tone === 'record' ? '● ' : ''}${status.text}`;
  return <div className={styles.recording}>
    <span className={styles.recordingTime}><b>{time}</b>{range && <small>{range}</small>}</span>
    <button type="button" className={styles.recordingOpen} onClick={onPress}>
      <span className={styles.recordingArt}>{artwork && <Artwork source={artwork} alt=""/>}</span>
      <span className={styles.recordingWords}><b>{title}</b>
        {(flags?.length || detail) && <span className={styles.recordingLine}>{flags?.map(flag => <Flag key={flag.id} flag={flag}/>)}{detail && <span>{detail}</span>}</span>}
        {(channel || source) && <span className={styles.recordingLine} data-quiet>{channel && <><ChannelMark mark={channel.mark} size={20}/><span>{channel.name}</span></>}{source && <span>{source}</span>}</span>}
        {/* At phone width the status is a line of the row's words (phone-app 12), not a column. */}
        {statusText && <span className={styles.recordingStatus} data-tone={status?.tone} data-inline>{statusText}</span>}
      </span>
    </button>
    {statusText && <span className={styles.recordingStatus} data-tone={status?.tone}>{statusText}</span>}
    {more && <Button variant="icon" icon="i-more" iconOnly aria-label={more.label} onClick={more.onPress}/>}
  </div>;
}

export type ConflictCardProps = Readonly<{title: string; body: string; children?: ReactNode; action?: Readonly<{label: string; onPress: () => void}>}>;
/** Recordings' conflict (desktop-live live-upcoming, phone-app 12): a card ruled in the record colour. */
export function ConflictCard({title, body, children, action}: ConflictCardProps) {
  return <div className={styles.conflict}><div className={styles.conflictHead}><Icon name="i-warn" size={20}/><span><b>{title}</b><small>{body}</small></span></div>
    {children}{action && <Button size="small" onClick={action.onPress}>{action.label}</Button>}</div>;
}
