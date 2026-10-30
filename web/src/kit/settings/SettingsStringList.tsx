import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button, TextField} from '../controls';
import {Icon} from '../Icon';
import styles from './settings.module.css';

export function SettingsStringList({label, values, addLabel, disabled, onChange}: {label: string; values: readonly string[]; addLabel: string; disabled?: boolean; onChange: (values: readonly string[]) => void}) {
  const [draft, setDraft] = useState('');
  return <div className={styles.stringList}><ul aria-label={label}>{values.map(value => <li key={value}><span>{value}</span><Button size="xs" variant="icon" disabled={disabled} aria-label={defaultI18n.t('next.settings.remove', {name: value})} onClick={() => onChange(values.filter(item => item !== value))}><Icon name="i-x" size={16} /></Button></li>)}</ul>
    <form onSubmit={event => {event.preventDefault(); const value = draft.trim(); if (value && !values.includes(value) && !disabled) {onChange([...values, value]); setDraft('');}}}>
      <TextField aria-label={addLabel} value={draft} disabled={disabled} onChange={event => setDraft(event.currentTarget.value)} />
      <Button type="submit" size="compact" disabled={disabled || !draft.trim() || values.includes(draft.trim())}>{addLabel}</Button>
    </form>
  </div>;
}
