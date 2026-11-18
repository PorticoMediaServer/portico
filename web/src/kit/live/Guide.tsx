import type {CSSProperties, ReactNode} from 'react';
import {ChannelMark} from '../artwork';
import {frameSpec} from '@portico/design';
import {useFrameForm} from '../overlays/shared';
import styles from './live.module.css';

export type GuideMark = Readonly<{sourceId: string; id: string; text: string; seed: string}>;
export type GuideChannel = Readonly<{id: string; name: string; number?: string; favourite?: boolean; mark: GuideMark; cells: readonly GuideCell[]}>;
export type GuideFlag = Readonly<{id: 'live' | 'new' | 'premiere'; text: string}>;
/** One programme, placed by minutes from the guide's left edge (`from` below 0 began earlier). */
export type GuideCell = Readonly<{id: string; title: string; detail?: string; from: number; to: number; flags?: readonly GuideFlag[];
  recording?: string; past?: boolean; empty?: boolean; selected?: boolean; progress?: number}>;
export type GuideGridProps = Readonly<{
  channels: readonly GuideChannel[]; span: number; now: number;
  ruler: readonly Readonly<{at: number; label: string}>[]; nowLabel: string; channelLabel: string;
  onCell?: (channel: GuideChannel, cell: GuideCell) => void;
}>;
const minutes = (value: number) => ({'--at': value} as CSSProperties);

/** The Live TV guide grid (desktop-live live-guide, phone-guide g1, tablet-play 6): a channel column that stays put, a
 * time ruler with now marked on it, one row of programmes per channel that scrolls sideways, crossed by the now-line.
 * The native kit has the same part. The live guide (`screens/live/GuideScreen.tsx`) still draws its own; Stage 2 moves it here. */
export function GuideGrid({channels, span, now, ruler, nowLabel, channelLabel, onCell}: GuideGridProps) {
  const form = useFrameForm(), phone = form === 'phone', px = form === 'expanded' ? frameSpec.guide.perMinute.web : frameSpec.guide.perMinute[form];
  // A ruler time the now pill would sit on is left out (phone-guide g1, desktop-live: 3:30, 4:05 now, 4:30), so no
  // half-covered label shows beside the pill: a label's width before now, the pill's half after it, in the ruler's text size (13).
  const underNow = (at: number) => (at - now) * px > -13 * 6.5 && (at - now) * px < 13 * 2.4;
  return <div className={styles.guide} style={{'--span': span, '--now': now} as CSSProperties}>
    <div className={styles.guideChannels}>
      <div className={styles.guideRuler}>{!phone && channelLabel}</div>
      {channels.map(channel => <div key={channel.id} className={styles.guideChannel}>
        <ChannelMark mark={channel.mark} size={phone ? 36 : 40}/>
        {phone ? channel.number && <small>{channel.number}</small> : <span><b>{channel.name}{channel.favourite && <i aria-hidden="true"> ★</i>}</b>{channel.number && <small>{channel.number}</small>}</span>}
      </div>)}
    </div>
    <div className={styles.guideScroll}>
      <div className={styles.guideLane}>
        <div className={styles.guideRuler}>{ruler.filter(mark => !underNow(mark.at)).map(mark => <span key={mark.at} style={minutes(mark.at)}>{mark.label}</span>)}<b className={styles.guideNow}>{nowLabel}</b></div>
        {channels.map(channel => <div key={channel.id} className={styles.guideRow}>{channel.cells.map(cell => <button key={cell.id} type="button" className={styles.guideCell}
          style={{'--from': Math.max(0, cell.from), '--to': Math.min(span, cell.to)} as CSSProperties} data-past={cell.past || undefined} data-empty={cell.empty || undefined} data-selected={cell.selected || undefined}
          onClick={onCell && (() => onCell(channel, cell))}>
          <span className={styles.guideTitle}>{cell.from < 0 && <i className={styles.clip} aria-hidden="true">‹</i>}{cell.recording && <i className={styles.dot} aria-hidden="true"/>}<b>{cell.title}</b></span>
          {(cell.detail || cell.flags?.length || cell.recording) && <span className={styles.guideDetail}>{cell.recording && <em>{cell.recording}</em>}{cell.flags?.map(flag => <Flag key={flag.id} flag={flag}/>)}{cell.detail && <span>{cell.detail}</span>}</span>}
          {cell.progress !== undefined && <span className={styles.progress} aria-hidden="true"><i style={{width: `${Math.round(cell.progress * 100)}%`}}/></span>}
        </button>)}</div>)}
        <span className={styles.guideLine} aria-hidden="true"/>
      </div>
    </div>
  </div>;
}

/** Live, New, Premiere. */
export function Flag({flag}: {flag: GuideFlag}) {
  return <span className={styles.flag} data-flag={flag.id}>{flag.text}</span>;
}

export type ChannelRowProps = Readonly<{channel: Omit<GuideChannel, 'cells'>; title: string; left?: string; progress?: number; live?: string; next?: string; onPress?: () => void}>;
/** The channel list (phone-app 10, desktop-live live-list): mark and number, the channel, what is on with the time left and a bar, then what is next. */
export function ChannelRow({channel, title, left, progress, live, next, onPress}: ChannelRowProps) {
  const phone = usePhone();
  return <button type="button" className={styles.channelRow} onClick={onPress}>
    <span className={styles.channelMark}><ChannelMark mark={channel.mark} size={phone ? 46 : 52}/>{channel.number && <small>{channel.number}</small>}</span>
    <span className={styles.channelWords}>
      <span className={styles.channelLine}><span>{channel.name}</span>{live && <em>● {live}</em>}{left && <small>{left}</small>}</span>
      <b>{title}</b>
      {progress !== undefined && <span className={styles.progress} data-inline aria-hidden="true"><i style={{width: `${Math.round(progress * 100)}%`}}/></span>}
      {next && <small>{next}</small>}
    </span>
  </button>;
}

export type AiringRowProps = Readonly<{time: string; title: string; code?: string; detail?: string; trailing?: ReactNode; onPress?: () => void}>;
/** One airing in a list (More airings and Later on in the programme pane, Coming up in a rule). */
export function AiringRow({time, title, code, detail, trailing, onPress}: AiringRowProps) {
  return <div className={styles.airing}><button type="button" onClick={onPress}><span className={styles.airingTime}>{time}</span>
    <span className={styles.airingWords}><b>{code && <strong>{code}</strong>}{title}</b>{detail && <small>{detail}</small>}</span></button>{trailing}</div>;
}
