import {calendarDate, calendarMonth, defaultI18n} from '@i18n';
import type {ReadingDayChoice} from '@portico/client-core';
import {Button} from '../../kit/controls';
import styles from './newspaper-calendar.module.css';

/** Calendar geometry is real; only server-admitted dates become actions. */
export function NewspaperCalendar({month, days, selected, onSelect}: Readonly<{month: string; days: readonly ReadingDayChoice[]; selected?: string; onSelect: (date: string) => void}>) {
  const calendar = calendarMonth(defaultI18n, month);
  if (!calendar) return null;
  const available = new Map(days.map(day => [day.value, day.count]));
  const cells = [...Array.from({length: calendar.offset}, () => undefined), ...calendar.dates];
  const rows = Array.from({length: Math.ceil(cells.length / 7)}, (_, row) => Array.from({length: 7}, (_, column) => cells[row * 7 + column]));
  return <table className={styles.calendar}>
    <caption>{calendar.label}</caption>
    <thead><tr>{calendar.weekdays.map((day, index) => <th key={index} scope="col">{day}</th>)}</tr></thead>
    <tbody>{rows.map((row, index) => <tr key={index}>{row.map((date, column) => <td key={column}>{date && <Button variant={selected === date ? 'primary' : 'ghost'}
      disabled={!available.has(date)} aria-pressed={selected === date}
      aria-label={available.has(date) ? `${calendarDate(defaultI18n, date)} · ${defaultI18n.t('next.reader.issueCount', {count: available.get(date)!})}` : calendarDate(defaultI18n, date)}
      onClick={() => {if (available.has(date)) onSelect(date);}}>{defaultI18n.number(Number(date.slice(-2)))}</Button>}</td>)}</tr>)}</tbody>
  </table>;
}
