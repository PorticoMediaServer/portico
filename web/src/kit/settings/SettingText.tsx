import {useEffect, useState} from 'react';
import {TextField} from '../controls';

/** Keep typing private to the input; a completed field makes one save intent. */
export function SettingText({label, value, placeholder, disabled, secret = false, onCommit}: {label: string; value: string; placeholder?: string; disabled?: boolean; secret?: boolean; onCommit: (value: string) => void}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => {setDraft(value);}, [value]);
  const commit = () => {if (!disabled && draft !== value) onCommit(draft);};
  return <TextField variant="setting" aria-label={label} value={draft} type={secret ? 'password' : 'text'} autoComplete={secret ? 'new-password' : undefined} disabled={disabled} placeholder={placeholder}
    onChange={event => setDraft(event.currentTarget.value)} onBlur={commit} onKeyDown={event => {if (event.key === 'Enter') {event.preventDefault(); event.currentTarget.blur();} else if (event.key === 'Escape') setDraft(value);}} />;
}
