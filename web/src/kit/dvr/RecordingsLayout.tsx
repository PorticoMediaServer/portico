import type {ReactNode} from 'react';
import {Select} from '../controls';
import {RuleGap} from './RuleCard';
import {PushedNavBar} from '../shell';
import {LibraryHeader} from '../browse';
import {useWindowWidth} from '../theme';
import styles from './rules.module.css';
/** Shared recordings page geometry, including its source header and ambient palette. */
export function RecordingsLayout({title, sourceLabel, allChannels, sourceName, channels, through, navigation, tabs, back, children}: {
  title: string; sourceLabel: string; allChannels: string; sourceName: string; channels?: string; through?: string; navigation?: ReactNode;
  /** The Guide | Recordings tabs: under the title in the shared page header from 600 px. */
  tabs?: ReactNode;
  /** phone-app 12: on a phone Recordings is a sub-page of Live TV, "‹ Live TV" and its title, in place of the Live TV
   * head and the Guide | Recordings tabs. */
  back?: Readonly<{label: string; title: string; onBack: () => void}>;
  children: ReactNode;
}) {
  const compact = useWindowWidth() < 600;
  const source = <Select className={styles.source} aria-label={sourceLabel} value="all" options={[{value: 'all', label: allChannels}]}/>;
  const facts = <p><strong>{sourceName}</strong>{channels && <><RuleGap/>{channels}</>}{through && <><RuleGap/>{through}</>}</p>;
  return <section className={styles.page} data-live-rules>{navigation}
    {back && <div className={styles.phoneNav}><PushedNavBar safeArea={false} backLabel={back.label} title={back.title} onBack={back.onBack}/></div>}
    {compact ? <><header className={styles.header}><h1>{title}</h1>{source}{facts}</header>{tabs}</>
      : <LibraryHeader className={styles.wideHeader} name={title} switcher={source} menu={facts}>{tabs}</LibraryHeader>}{children}</section>;
}
export function RecordingStorage({used, free, remaining, label, fraction}: {used: string; free: string; remaining: string; label: string; fraction: number}) {
  return <div className={styles.storage} role="img" aria-label={label}><span><b>{used}</b><RuleGap/>{free}</span><span className={styles.meter}><i style={{width: `${Math.round(Math.min(1, Math.max(0, fraction)) * 1000) / 10}%`}}/></span><small>{remaining}</small></div>;
}
