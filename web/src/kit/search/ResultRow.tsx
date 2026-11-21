import type {HTMLAttributes, ReactNode, Ref} from 'react';
import styles from './search.module.css';

export type ResultRowShape = 'poster' | 'square' | 'wide' | 'portrait';
export type ResultRowProps = Readonly<{
  /** The picture, drawn whole in its shape: a film's or show's poster, an album's or audiobook's square, an episode's still. */
  art: ReactNode; shape?: ResultRowShape;
  title: string;
  /** The facts under the title ("Movie · 2019 · 3 h 1 min", "Solo Leveling · S1 E3"). */
  facts?: ReactNode;
  /** At the row's end inside its button: a song's length. */
  end?: ReactNode;
  onPress: () => void;
  /** Before the row: its selection circle while selecting. */
  lead?: ReactNode;
  /** After the row: its ⋯. */
  more?: ReactNode;
  selected?: boolean; preview?: boolean;
  rootRef?: Ref<HTMLDivElement>;
  rootProps?: HTMLAttributes<HTMLDivElement>;
}>;

/** One search result as a row (final3-mobileweb-02, phone-app 8): the picture, the title and its facts, then ⋯. A phone's
 * search lists a group's first results this way instead of a shelf. */
export function ResultRow({art, shape = 'poster', title, facts, end, onPress, lead, more, selected, preview, rootRef, rootProps}: ResultRowProps) {
  return <div ref={rootRef} className={styles.resultRow} data-selected={selected || undefined} data-preview={preview || undefined} {...rootProps}>
    {lead}
    <button type="button" className={styles.resultOpen} onClick={onPress}>
      <span className={styles.resultArt} data-shape={shape}>{art}</span>
      <span className={styles.resultWords}><b>{title}</b>{facts && <small>{facts}</small>}</span>
      {end !== undefined && <span className={styles.resultEnd}>{end}</span>}
    </button>
    {more}
  </div>;
}
