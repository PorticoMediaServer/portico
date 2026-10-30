import type {ReactNode} from 'react';
import {Select} from '../controls';
import {RuleGap} from './RuleCard';
import styles from './rules.module.css';
/** Shared recordings page geometry, including its source header and ambient palette. */
export function RecordingsLayout({title, sourceLabel, allChannels, sourceName, channels, through, navigation, children}: {
  title: string; sourceLabel: string; allChannels: string; sourceName: string; channels?: string; through?: string; navigation?: ReactNode; children: ReactNode;
}) {
  return <section className={styles.page} data-live-rules>{navigation}
    <header className={styles.header}><h1>{title}</h1><Select className={styles.source} aria-label={sourceLabel} value="all" options={[{value: 'all', label: allChannels}]}/>
      <p><strong>{sourceName}</strong>{channels && <><RuleGap/>{channels}</>}{through && <><RuleGap/>{through}</>}</p></header>{children}</section>;
}
export function RecordingStorage({used, free, remaining, label, fraction}: {used: string; free: string; remaining: string; label: string; fraction: number}) {
  return <div className={styles.storage} role="img" aria-label={label}><span><b>{used}</b><RuleGap/>{free}</span><span className={styles.meter}><i style={{width: `${Math.round(Math.min(1, Math.max(0, fraction)) * 1000) / 10}%`}}/></span><small>{remaining}</small></div>;
}
