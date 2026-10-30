import {Icon} from '../../../kit/Icon';
import styles from './source-done.module.css';

export type SourceDoneRow = Readonly<{label: string; detail?: string}>;

export type SourceDoneProps = Readonly<{
  /** The caller's already-formatted completion title. */
  title: string;
  /** The caller's own lede, or nothing. */
  lede?: string;
  /** The caller's own rows, each built from a real accepted fact. */
  rows: readonly SourceDoneRow[];
}>;

/** Step 5's completion tile (remote-sources.html#6, .donehero/.chk-l). It draws
 * only the rows its caller already built from the accepted receipt, profile and
 * list row, so a fact with no producer is simply never rendered. */
export function SourceDone({title, lede, rows}: SourceDoneProps) {
  return <div className={styles.done}>
    <div className={styles.hero}>
      <span className={styles.big} aria-hidden="true"><Icon name="i-okc" size={34}/></span>
      <div>
        <h3>{title}</h3>
        {lede && <p>{lede}</p>}
      </div>
    </div>
    <ul className={styles.rows}>
      {rows.map((row, index) => <li key={index}>
        <span className={styles.mark} aria-hidden="true"><Icon name="i-check" size={15}/></span>
        <div><b>{row.label}</b>{row.detail && <small>{row.detail}</small>}</div>
      </li>)}
    </ul>
  </div>;
}
