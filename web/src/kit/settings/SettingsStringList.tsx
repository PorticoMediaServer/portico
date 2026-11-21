import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button, TextField} from '../controls';
import {Icon} from '../Icon';
import styles from './settings.module.css';

/** A short list of words or addresses as chips, each with its ×, then "+ Add …" (settings-profile Hidden labels,
 * desktop-settings remote Home networks). "+ Add …" opens a field in its place; Enter adds, Escape or an empty blur closes. */
export function SettingsStringList({label, values, addLabel, disabled, mono = false, onChange}: {label: string; values: readonly string[]; addLabel: string; disabled?: boolean;
  /** Addresses and network ranges read in the monospace face, as the mock-up draws them. */
  mono?: boolean; onChange: (values: readonly string[]) => void}) {
  const [draft, setDraft] = useState(''), [adding, setAdding] = useState(false);
  const add = () => {const value = draft.trim(); if (value && !values.includes(value) && !disabled) {onChange([...values, value]); setDraft(''); setAdding(false);}};
  return <div className={styles.stringList} data-mono={mono || undefined}>
    <ul aria-label={label}>{values.map(value => <li key={value} className={styles.stringChip}><span>{value}</span><button type="button" disabled={disabled} aria-label={defaultI18n.t('next.settings.remove', {name: value})} onClick={() => onChange(values.filter(item => item !== value))}><Icon name="i-x" size={14} /></button></li>)}
      <li>{adding ? <form className={styles.stringAdding} onSubmit={event => {event.preventDefault(); add();}}>
        <TextField aria-label={addLabel} value={draft} disabled={disabled} autoFocus onChange={event => setDraft(event.currentTarget.value)}
          onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); setDraft(''); setAdding(false);}}} onBlur={() => {if (!draft.trim()) setAdding(false);}} />
        <Button type="submit" size="small" disabled={disabled || !draft.trim() || values.includes(draft.trim())}>{addLabel}</Button>
      </form> : <button type="button" className={styles.stringAdd} disabled={disabled} onClick={() => setAdding(true)}><Icon name="i-plus" size={16} />{addLabel}</button>}</li>
    </ul>
  </div>;
}
