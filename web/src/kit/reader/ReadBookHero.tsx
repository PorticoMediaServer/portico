import {useState, type ReactNode} from 'react';
import {Tabs} from '../controls';
import {Icon} from '../Icon';
import {useWindowWidth} from '../theme';
import styles from './book.module.css';

export type ReadDetailFact = Readonly<{id: string; label: string; value: ReactNode; source?: string}>;
export function ReadBookFacts({heading, facts}: Readonly<{heading: string; facts: readonly ReadDetailFact[]}>) {
  if (facts.length === 0) return null;
  return <section className={styles.details}><h2>{heading}</h2><dl>{facts.map(fact => <div key={fact.id}><dt>{fact.label}</dt><dd>{fact.value}{fact.source && <small>{fact.source}</small>}</dd></div>)}</dl></section>;
}

/** The reader's own highlights and bookmarks on a book's page, with the way into the reader
 * (desktop-read 3 under Contents; phone-read 8 at the top of the Contents tab). */
export function ReadMarksStrip({text, action, onOpen}: Readonly<{text: string; action: string; onOpen: () => void}>) {
  return <button type="button" className={styles.marks} onClick={onOpen} aria-label={`${text} ${action}`}>
    <Icon name="i-highlight" size={22}/><span className={styles.marksText}>{text}</span>
    <span className={styles.marksAction} aria-hidden="true">{action}</span><Icon className={styles.marksArrow} name="i-right" size={18}/></button>;
}

export function ReadBookSections({contents: list, marks, details, labels}: Readonly<{contents?: ReactNode; marks?: ReactNode; details?: ReactNode; labels: Readonly<{contents: string; details: string}>}>) {
  const width = useWindowWidth(), [selected, setSelected] = useState('contents');
  const contents = list || marks ? <>{list}{marks && <div className={styles.marksSlot}>{marks}</div>}</> : undefined;
  if (!contents && !details) return null;
  if (width < 1024) return <Tabs className={styles.compactSections} listClassName={styles.sectionTabs} phone={width < 600}
    value={selected === 'contents' && contents ? 'contents' : details ? 'details' : 'contents'} onValueChange={setSelected}
    items={[...(contents ? [{value: 'contents', label: labels.contents, panel: contents}] : []),
      ...(details ? [{value: 'details', label: labels.details, panel: details}] : [])]}/>;
  return <div className={styles.sections}>{contents && <section>{contents}</section>}{details}</div>;
}
