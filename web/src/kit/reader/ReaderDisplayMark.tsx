import styles from './display.module.css';

/** Drawn Aa artwork, with the exact typography owned by the reader kit. */
export function ReaderDisplayMark({sample}: {sample: string}) {
  const letters = Array.from(sample);
  return <span className={styles.displayMark}>{letters[0]}<small>{letters.slice(1).join('')}</small></span>;
}
