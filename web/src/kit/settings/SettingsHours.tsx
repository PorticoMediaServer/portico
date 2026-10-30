import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ScheduleWindow} from '@core/server-administration.ts';
import {daysOfChoice, dayChoiceOf, newViewingHours, VIEWING_HOURS_MAX, type DayChoice} from '@core/server-admin/people-words.ts';
import {Button, Select, TextField} from '../controls';
import {Icon} from '../Icon';
import styles from './settings.module.css';

const t = defaultI18n.t;
const clock = (minutes: number) => `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
const minute = (value: string) => {const match = /^(\d{2}):(\d{2})$/.exec(value); return match && Number(match[1]) < 24 && Number(match[2]) < 60 ? Number(match[1]) * 60 + Number(match[2]) : undefined;};
function Window({value, disabled, change, remove}: {value: ScheduleWindow; disabled?: boolean; change: (value: ScheduleWindow) => void; remove: () => void}) {
  const [start, setStart] = useState(clock(value.startMinute)), [end, setEnd] = useState(clock(value.endMinute));
  useEffect(() => {setStart(clock(value.startMinute)); setEnd(clock(value.endMinute));}, [value]);
  const commit = () => {const a = minute(start), b = minute(end); if (a !== undefined && b !== undefined && a < b && (a !== value.startMinute || b !== value.endMinute)) change({...value, startMinute: a, endMinute: b}); else {setStart(clock(value.startMinute)); setEnd(clock(value.endMinute));}};
  const days = [
    {value: 'all', label: t('next.settings.everyDay')}, {value: 'weekdays', label: t('next.settings.weekdays')}, {value: 'weekends', label: t('next.settings.weekends')},
    ...Array.from({length: 7}, (_, index) => ({value: String(index), label: new Intl.DateTimeFormat(undefined, {weekday: 'long', timeZone: 'UTC'}).format(new Date(Date.UTC(2026, 0, 4 + index)))})),
    ...(dayChoiceOf(value.days) === 'several' ? [{value: 'several', label: t('next.settings.selectedDays')}] : []),
  ];
  return <div className={styles.hoursWindow}>
    <Select aria-label={t('next.settings.days')} disabled={disabled} options={days} value={dayChoiceOf(value.days)} onValueChange={choice => change({...value, days: daysOfChoice(choice as DayChoice, value.days)})} />
    <TextField aria-label={t('next.settings.starts')} disabled={disabled} type="time" value={start} onChange={event => setStart(event.currentTarget.value)} onBlur={commit} />
    <span>{t('next.settings.until')}</span><TextField aria-label={t('next.settings.ends')} disabled={disabled} type="time" value={end} onChange={event => setEnd(event.currentTarget.value)} onBlur={commit} />
    <Button variant="icon" size="small" disabled={disabled} aria-label={t('next.settings.removeHours')} onClick={remove}><Icon name="i-x" size={16} /></Button>
  </div>;
}
export function SettingsHours({windows, disabled, onChange}: {windows: readonly ScheduleWindow[]; disabled?: boolean; onChange: (windows: readonly ScheduleWindow[]) => void}) {
  return <div className={styles.hours}>{windows.map((value, index) => <Window key={index} value={value} disabled={disabled} change={next => onChange(windows.map((value, position) => position === index ? next : value))} remove={() => onChange(windows.filter((_, position) => position !== index))} />)}
    <Button size="compact" disabled={disabled || windows.length >= VIEWING_HOURS_MAX} onClick={() => onChange([...windows, newViewingHours()])}>{t('next.settings.addHours')}</Button>
  </div>;
}
