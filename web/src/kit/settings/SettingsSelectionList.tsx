import styles from './settings.module.css';

export function SettingsSelectionList({label, items, selected, onChange, disabled, minimum = 0}: {label: string; items: readonly {id: string; label: string}[]; selected: readonly string[]; onChange: (ids: readonly string[]) => void; disabled?: boolean; minimum?: number}) {
  return <fieldset className={styles.selectionList} disabled={disabled}><legend>{label}</legend>{items.map(item => <label key={item.id}>
    <input type="checkbox" disabled={selected.length <= minimum && selected.includes(item.id)} checked={selected.includes(item.id)} onChange={event => onChange(event.currentTarget.checked ? [...selected, item.id] : selected.filter(id => id !== item.id))} />
    <span>{item.label}</span>
  </label>)}</fieldset>;
}
