import type {ReactNode} from 'react';
import styles from './metadata-facts.module.css';

/** Adjacent metadata keeps its semantic order, grouped by space rather than punctuation. */
export function MetadataFacts({parts, className}: {parts: readonly ReactNode[]; className?: string}) {
  return <span className={[styles.facts, className].filter(Boolean).join(' ')}>{parts.filter(part => part !== undefined && part !== null && part !== false && part !== '').map((part, index) => <span key={index}>{part}</span>)}</span>;
}
