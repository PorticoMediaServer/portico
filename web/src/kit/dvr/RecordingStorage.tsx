import {RuleGap} from './RuleCard';
import styles from './rules.module.css';

/** The recordings storage line: used and free, the meter and the hours left (desktop-live live-recordings). */
export function RecordingStorage({used, free, remaining, label, fraction}: {used: string; free: string; remaining: string; label: string; fraction: number}) {
  return <div className={styles.storage} role="img" aria-label={label}><span><b>{used}</b><RuleGap/>{free}</span><span className={styles.meter}><i style={{width: `${Math.round(Math.min(1, Math.max(0, fraction)) * 1000) / 10}%`}}/></span><small>{remaining}</small></div>;
}
