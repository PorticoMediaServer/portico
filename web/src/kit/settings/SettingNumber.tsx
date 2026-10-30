import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {TextField, Button} from '../controls';
import {Icon} from '../Icon';
import styles from './settings.module.css';

export type SettingNumberProps = Readonly<{label: string; value: number | null; optional?: boolean; min: number; max: number; step?: number; unit?: string; placeholder?: string; disabled?: boolean; plain?: boolean; /** Presentation only: the field shell carries the authored changed ring. */ changed?: boolean; onCommit: (value: number | null) => void}>;
/** Empty optional fields stay empty. In-progress typing is never sent as NaN. */
export function SettingNumber({label, value, optional, min, max, step = 1, unit, placeholder, disabled, plain = false, changed = false, onCommit}: SettingNumberProps) {
  const [draft, setDraft] = useState(value === null ? '' : String(value));
  useEffect(() => {setDraft(value === null ? '' : String(value));}, [value]);
  const commit = (text: string) => {
    if (!text.trim() && optional) {setDraft(''); if (value !== null) onCommit(null); return;}
    const numeric = Number(text);
    if (!text.trim() || !Number.isFinite(numeric)) {setDraft(value === null ? '' : String(value)); return;}
    const next = Math.min(max, Math.max(min, numeric));
    setDraft(String(next)); if (next !== value) onCommit(next);
  };
  const change = (direction: number) => commit(String(Math.round(((value ?? min) + direction * step) * 1000000) / 1000000));
  return <span className={styles.number} data-plain={plain || undefined}>
    {!plain && <Button size="xs" variant="icon" disabled={disabled || value !== null && value <= min} aria-label={defaultI18n.t('next.settings.decrease', {name: label})} onClick={() => change(-1)}><span aria-hidden="true">−</span></Button>}
    <TextField variant="setting" changed={changed} aria-label={label} type="text" inputMode="decimal" value={draft} placeholder={placeholder} disabled={disabled} onChange={event => setDraft(event.currentTarget.value)}
      onBlur={event => commit(event.currentTarget.value)} onKeyDown={event => {
        if (event.key === 'Enter') {event.preventDefault(); commit(draft);}
        if (event.key === 'Escape') {event.preventDefault(); setDraft(value === null ? '' : String(value));}
        if (['ArrowUp', 'ArrowDown'].includes(event.key)) {event.preventDefault(); change(event.key === 'ArrowUp' ? 1 : -1);}
      }} />
    {unit && <span>{unit}</span>}
    {!plain && <Button size="xs" variant="icon" disabled={disabled || value !== null && value >= max} aria-label={defaultI18n.t('next.settings.increase', {name: label})} onClick={() => change(1)}><Icon name="i-plus" size={16} /></Button>}
  </span>;
}
