import {useState, type ReactNode} from 'react';
import {Tabs} from '../controls';
import {useWindowWidth} from '../theme';
import styles from './book.module.css';

export type ReadDetailFact = Readonly<{id: string; label: string; value: ReactNode; source?: string}>;
export function ReadBookFacts({heading, facts}: Readonly<{heading: string; facts: readonly ReadDetailFact[]}>) {
  if (facts.length === 0) return null;
  return <section className={styles.details}><h2>{heading}</h2><dl>{facts.map(fact => <div key={fact.id}><dt>{fact.label}</dt><dd>{fact.value}{fact.source && <small>{fact.source}</small>}</dd></div>)}</dl></section>;
}

export function ReadBookSections({contents, details, labels}: Readonly<{contents?: ReactNode; details?: ReactNode; labels: Readonly<{contents: string; details: string}>}>) {
  const width = useWindowWidth(), [selected, setSelected] = useState('contents');
  if (!contents && !details) return null;
  if (width < 1024) return <Tabs className={styles.compactSections} listClassName={styles.sectionTabs} phone={width < 600}
    value={selected === 'contents' && contents ? 'contents' : details ? 'details' : 'contents'} onValueChange={setSelected}
    items={[...(contents ? [{value: 'contents', label: labels.contents, panel: contents}] : []),
      ...(details ? [{value: 'details', label: labels.details, panel: details}] : [])]}/>;
  return <div className={styles.sections}>{contents && <section>{contents}</section>}{details}</div>;
}
