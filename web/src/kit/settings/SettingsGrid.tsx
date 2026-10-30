import type {CSSProperties} from 'react';
import type {SettingsTableColumn, SettingsTableRow} from './SettingsTable';
import styles from './settings.module.css';

/** Small measured hardware grid. The adapter supplies all cells; this owns no probe data. */
export function SettingsGrid({label, columns, rows}: {label: string; columns: readonly SettingsTableColumn[]; rows: readonly SettingsTableRow[]}) {
  return <div className={styles.settingsGrid}><table className={styles.table} aria-label={label}>
    <colgroup>{columns.map(column => <col key={column.id} style={{width: column.width} as CSSProperties} />)}</colgroup>
    <thead><tr>{columns.map(column => <th key={column.id} scope="col">{column.label}</th>)}</tr></thead>
    <tbody>{rows.map(row => <tr key={row.id}>{columns.map(column => {
      const value = row.cells[column.id], cell = typeof value === 'string' ? {title: value} : value;
      return <td key={column.id}>{cell?.title ? <span className={column.kind === 'badge' ? styles.badge : undefined} data-tone={cell.tone}>{cell.title}</span> : null}</td>;
    })}</tr>)}</tbody>
  </table></div>;
}
