import type {ReactNode} from 'react';
import {Icon, type IconName} from '../Icon';
import styles from './directory.module.css';

export type DirectoryEntry = Readonly<{id: string; label: string; icon: IconName; onPress: () => void;
  /** A quieter line under the label: a pin's kind, "2 ready to save, 1 preparing". */ caption?: ReactNode;
  /** The count before the chevron, already formatted. */ count?: string}>;
/** Saved at phone width (phone-app 9, phone-web 6): one rounded card of rows, each an icon tile, its label, a count and a
 * chevron, each opening its own pushed page. */
export function DirectoryRows({rows, label}: {rows: readonly DirectoryEntry[]; label?: string}) {
  return <div className={styles.rows} role="list" aria-label={label}>{rows.map(row => <div key={row.id} role="listitem">
    <button type="button" className={styles.row} onClick={row.onPress}>
      <span className={styles.icon}><Icon name={row.icon} size={18} /></span>
      <span className={styles.label}>{row.label}{row.caption && <small className={styles.caption}>{row.caption}</small>}</span>
      {row.count !== undefined && <span className={styles.count}>{row.count}</span>}
      <Icon name="i-right" size={18} className={styles.chevron} />
    </button>
  </div>)}</div>;
}
