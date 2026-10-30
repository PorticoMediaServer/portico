import {Fragment} from 'react';
import {locale} from './text';
import styles from './detail.module.css';

export function GenreNames({names, onOpen}: {names: readonly string[]; onOpen?: (genre: string) => void}) {
  return <>{new Intl.ListFormat(locale(), {type: 'unit', style: 'long'}).formatToParts(names).map((part, index) => part.type === 'element' && onOpen
    ? <button key={index} type="button" className={styles.inlineLink} onClick={() => onOpen(part.value)}>{part.value}</button>
    : <Fragment key={index}>{part.value}</Fragment>)}</>;
}
