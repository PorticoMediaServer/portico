import {useLayoutEffect, useRef, useState} from 'react';
import {createI18n} from '@i18n';
import {Button, Select} from '../controls';
import {BottomSheet} from '../overlays/BottomSheet';
import {usePhone} from '../overlays/shared';
import {readSegmentClock, segmentClock} from './model';
import type {ShellTranslate} from './ServerIdentity';
import {segmentEditKinds, type SegmentKind} from './SegmentWindows';
import styles from './segments.module.css';

const tDefault = createI18n().t;

/** owner-libraries frame 23a and Root §49: Add a marker opens the authored
 * editor as an empty required create draft. Both times start blank (blank is
 * not zero), there is no preselected type, and Save only enables a supported
 * type with 0 <= start < end <= the known duration. Nothing is written until
 * Save, and no interval is inferred from the clock, detector or neighbours. */
export function CreateMarkerSheet({duration, busy = false, onSave, onCancel, onPreviewAt, t = tDefault}: Readonly<{
  duration: number; busy?: boolean;
  onSave: (draft: Readonly<{kind: SegmentKind; startUS: string; endUS: string}>) => void;
  onCancel: () => void;
  onPreviewAt: (start: number) => void;
  t?: ShellTranslate;
}>) {
  const phone = usePhone();
  const [kind, setKind] = useState<SegmentKind | undefined>(undefined);
  const [startText, setStartText] = useState(''), [endText, setEndText] = useState('');
  const start = readSegmentClock(startText), end = readSegmentClock(endText);
  const bounded = duration > 0 && start !== undefined && end !== undefined && start >= 0 && start < end && end <= duration;
  const valid = kind !== undefined && bounded;
  // One opening-owned committed draft. Retained handlers read this lease's
  // current values, never their render's copy.
  type CreateLease = {kind: SegmentKind | undefined; start: number | undefined; end: number | undefined; valid: boolean; busy: boolean};
  const live = useRef<CreateLease | undefined>(undefined);
  const [lease, setLease] = useState<CreateLease | undefined>(undefined);
  useLayoutEffect(() => {
    const next: CreateLease = {kind: undefined, start: undefined, end: undefined, valid: false, busy: false};
    live.current = next;setLease(next);
    return () => {if (live.current === next) live.current = undefined;};
  }, []);
  useLayoutEffect(() => {
    const current = live.current;
    if (!current) return;
    current.kind = kind;current.start = start;current.end = end;current.valid = valid;current.busy = busy;
  });
  const same = () => !!lease && live.current === lease;
  const current = () => same() && !lease?.busy;
  const save = () => {
    if (!same() || !lease) return;
    const chosen = lease.kind, from = lease.start, to = lease.end;
    if (lease.busy || !lease.valid || chosen === undefined || from === undefined || to === undefined) return;
    onSave({kind: chosen, startUS: String(Math.round(from * 1e6)), endUS: String(Math.round(to * 1e6))});
  };
  const preview = () => {
    if (!same() || !lease || lease.busy) return;
    const from = lease.start;
    if (from === undefined || duration <= 0 || from < 0 || from > duration) return;
    onPreviewAt(Math.max(0, from - 3));
  };
  return <BottomSheet open onOpenChange={open => {if (!open) onCancel();}} title={t('web.segments.addTitle')} footerClassName={styles.sheetActs}
    footer={phone ? <><Button variant="primary" size="compact" disabled={busy || !valid} onClick={save}>{t('next.shell.save')}</Button><Button variant="glass" size="compact" disabled={busy} onClick={onCancel}>{t('next.shell.cancel')}</Button></> : <><Button variant="glass" size="compact" disabled={busy} onClick={onCancel}>{t('next.shell.cancel')}</Button><Button variant="primary" size="compact" disabled={busy || !valid} onClick={save}>{t('next.shell.save')}</Button></>}>
    <div className={styles.adjustSheet}>
      <Select label={t('web.segments.kindLabel')} placeholder={t('web.segments.chooseType')} value={kind} disabled={busy} options={segmentEditKinds.map(value => ({value, label: t('web.segments.kind', {kind: value})}))}
        onValueChange={value => {if (!current()) return;const next = segmentEditKinds.find(candidate => String(candidate) === value);if (next) setKind(next);}} />
      <div className={styles.timeline} aria-hidden="true" />
      <BlankTime label={t('next.shell.startsAt')} text={startText} value={start} duration={duration} disabled={busy} current={current} t={t} onText={setStartText} />
      <BlankTime label={t('next.shell.endsAt')} text={endText} value={end} duration={duration} disabled={busy} current={current} t={t} onText={setEndText} />
      <Button variant="glass" size="compact" disabled={busy || start === undefined || duration <= 0 || start < 0 || start > duration} onClick={preview}>{t('next.shell.previewSegment')}</Button>
    </div>
  </BottomSheet>;
}

/** A time that starts blank. A stepper cannot seed the blank; once the person
 * has entered a parsable time it steps and repeats on hold like the authored
 * stepper, and every release path stops it. */
function BlankTime({label, text, value, duration, disabled = false, current, onText, t = tDefault}: Readonly<{
  label: string; text: string; value: number | undefined; duration: number; disabled?: boolean; current: () => boolean; onText: (text: string) => void; t?: ShellTranslate;
}>) {
  const live = useRef(value), delay = useRef<number | undefined>(undefined), repeat = useRef<number | undefined>(undefined);
  useLayoutEffect(() => {live.current = value;});
  const stop = () => {window.clearTimeout(delay.current);window.clearInterval(repeat.current);delay.current = repeat.current = undefined;};
  useLayoutEffect(() => {
    window.addEventListener('blur', stop);
    return () => {window.removeEventListener('blur', stop);stop();};
  }, []);
  const step = (delta: number) => {
    if (!current()) return;
    const currentValue = live.current;
    if (currentValue === undefined) return;
    const next = Math.round(Math.max(0, Math.min(duration, currentValue + delta)) * 10) / 10;
    if (next === currentValue) return;
    live.current = next;onText(segmentClock(next, true));
  };
  useLayoutEffect(() => {if (disabled) stop();}, [disabled]);
  const begin = (event: React.PointerEvent<HTMLButtonElement>, delta: number) => {
    if (!current() || disabled || event.button !== 0 || live.current === undefined) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    stop();step(delta);
    delay.current = window.setTimeout(() => {repeat.current = window.setInterval(() => step(delta), 80);}, 400);
  };
  const button = (delta: -1 | 1) => <button type="button" className={styles.stepButton} disabled={disabled || value === undefined} aria-label={delta < 0 ? t('next.shell.oneSecondEarlier') : t('next.shell.oneSecondLater')}
    onPointerDown={event => begin(event, delta)} onPointerUp={stop} onPointerCancel={stop} onLostPointerCapture={stop}
    onClick={event => {if (event.detail === 0) step(delta);}}>{delta < 0 ? '−' : '+'}</button>;
  const invalid = text.trim().length > 0 && (value === undefined || value < 0 || value > duration);
  return <div className={styles.stepper}><span className={styles.stepperLabel}>{label}</span><div className={styles.stepperRow}>{button(-1)}
    <label className={styles.timeField}>{label}<input value={text} placeholder={t('web.segments.timePlaceholder')} inputMode="decimal" disabled={disabled} aria-invalid={invalid || undefined} onChange={event => {if (current()) onText(event.currentTarget.value);}} /></label>{button(1)}</div></div>;
}
