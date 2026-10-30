import type {ReactNode} from 'react';
import {Icon} from '../../../kit/Icon';
import styles from './source-header.module.css';

export type SourceHeaderProps = Readonly<{
  /** The caller's existing status badge node from the loaded source snapshot. */
  status: ReactNode;
  /** The section's own accessible name. */
  label: string;
  /** The drawn title, only on a surface that has no dialog title of its own. */
  name?: string;
  /** The source's own address, when the loaded row carries one. */
  origin?: string;
  /** One existing problem sentence, from the caller's own problem lookup. */
  notice?: string;
  /** The caller's own actions, unchanged. */
  actions?: ReactNode;
  /** The caller's real phone decision; never detected here. */
  phone?: boolean;
  /** The caller's own fact rows. */
  children?: ReactNode;
}>;

/** The shared top of a remote source (remote-sources.html#26/#27 and 7d): a
 * state badge, the source's address, one notice, the caller's actions and the
 * caller's own status rows. Presentation only: every fact, sentence and action
 * arrives already rendered from the caller's loaded snapshot, so this adds no
 * health, cache, schedule, connectivity or count. */
export function SourceHeader({status, label, name, origin, notice, actions, phone, children}: SourceHeaderProps) {
  return <section className={styles.header} data-phone={phone || undefined} aria-label={label}>
    <div className={styles.head}>
      {name && <h3 className={styles.name}>{name}</h3>}
      {status}
      {origin && <span className={styles.origin}>{origin}</span>}
      {actions && <span className={styles.actions}>{actions}</span>}
    </div>
    {notice && <p className={styles.notice}><Icon name="i-warn" size={20}/><span>{notice}</span></p>}
    {children}
  </section>;
}
