import type {ReactNode} from 'react';
import styles from './search-empty.module.css';

/** desktop-search s-none: the no-match sentence is left aligned under the search field. */
export function SearchEmpty({title, body, action}: {title: string; body?: string; action?: ReactNode}) {
  return <section className={styles.empty} role="status"><h2>{title}</h2>{body && <p>{body}</p>}{action}</section>;
}
