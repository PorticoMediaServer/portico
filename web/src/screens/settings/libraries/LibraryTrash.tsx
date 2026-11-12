import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ServerRequester} from '@core/query/server-api.ts';
import {emptyLibraryTrash, readLibraryTrash, removedAtNextScan, saveTrashRule, type LibraryTrash as Trash, type TrashRule} from '@core/admin/library-trash.ts';
import type {MessageId} from '@i18n';
import {Button, Select, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './libraries.module.css';
const t = defaultI18n.t;

/**
 * A library's missing titles, its trash (03-scanning rules 59 to 62): the library's own rule for emptying it, each
 * missing title with when it went missing and when it will be removed, and Empty trash once the count is typed.
 */
export function LibraryTrash({libraryId, api, isCurrent, label}: Readonly<{
  libraryId: string; api: ServerRequester; isCurrent: () => boolean; label: string;
}>) {
  const [open, setOpen] = useState(false), [trash, setTrash] = useState<Trash>(), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false);
  const [days, setDays] = useState(''), [confirming, setConfirming] = useState(false), [typed, setTyped] = useState(''), [removed, setRemoved] = useState<number>();
  const lifetime = useRef<AbortController | undefined>(undefined);
  const live = (capture: AbortController | undefined): capture is AbortController => !!capture && lifetime.current === capture && !capture.signal.aborted && isCurrent();
  const load = async (capture: AbortController) => {
    setBusy(true); setFailed(false);
    try { const next = await readLibraryTrash(api, libraryId, capture.signal); if (live(capture)) setTrash(next); }
    catch { if (live(capture)) setFailed(true); }
    finally { if (live(capture)) setBusy(false); }
  };
  // The effect reads through the latest `load`, published at commit (as SkippedFiles does).
  const ports = useRef({load});
  useEffect(() => {ports.current = {load};});
  useEffect(() => {
    if (!open) return;
    const capture = new AbortController(); lifetime.current = capture;
    setTrash(undefined); setRemoved(undefined); setConfirming(false); void ports.current.load(capture);
    return () => {capture.abort(); if (lifetime.current === capture) lifetime.current = undefined;};
  }, [open, libraryId, api]);
  const run = async (work: (signal: AbortSignal) => Promise<void>) => {
    const capture = lifetime.current;
    if (!live(capture) || busy) return;
    setBusy(true); setFailed(false);
    try { await work(capture.signal); } catch { if (live(capture)) setFailed(true); }
    finally { if (live(capture)) setBusy(false); }
  };
  const empty = () => trash && run(async signal => {
    const count = await emptyLibraryTrash(api, libraryId, typed.trim(), signal);
    if (!live(lifetime.current)) return;
    setConfirming(false); setTyped(''); setRemoved(count);
    await load(lifetime.current!);
  });
  const removal = (removedAt?: string) => !trash ? '' : trash.rule.mode === 'manual' || !removedAt ? '' : trash.rule.mode === 'scan'
    ? t('next.ownerLibraries.missingRemovedNextScan') : t('next.ownerLibraries.missingRemovedOn', {date: defaultI18n.date(removedAt)});
  return <>
    <Button variant="glass" size="small" onClick={() => {if (isCurrent()) setOpen(true);}}>{label}</Button>
    <Dialog open={open} onOpenChange={setOpen} size="lg" width={640} title={t('next.ownerLibraries.missingTitle')} description={t('next.ownerLibraries.missingLede')} footer={<>
      <Button onClick={() => setOpen(false)}>{t('next.overlays.close')}</Button>
      {trash && trash.total > 0 && <Button variant="primary" disabled={busy} onClick={() => {setTyped(''); setConfirming(true);}}>{t('next.ownerLibraries.emptyTrash')}</Button>}
    </>}>
      {failed ? <p role="alert">{t('next.states.pageFailedBody')}</p> : null}
      {!trash && busy ? <p role="status">{t('next.settings.loading')}</p> : null}
      {trash && <>
        {removed !== undefined && <p role="status">{t('next.ownerLibraries.emptyTrashDone', {count: removed})}</p>}
        {trash.items.length ? <ul className={styles.skippedList}>{trash.items.map(item => <li key={item.id}>
          <b>{item.year ? t('next.ownerLibraries.missingTitleYear', {title: item.title, year: String(item.year)}) : item.title}</b>
          <small>{t('next.ownerLibraries.missingSince', {date: defaultI18n.date(item.missingSince)})}</small>
          {removal(item.removedAt) && <small>{removal(item.removedAt)}</small>}
        </li>)}</ul> : <p className={styles.trashHelp}>{t('next.ownerLibraries.missingNone', {library: trash.name})}</p>}
        {trash.total > trash.items.length && <p className={styles.trashHelp}>{t('next.ownerLibraries.missingMore', {shown: trash.items.length, count: trash.total})}</p>}
      </>}
    </Dialog>
    <Dialog open={confirming && !!trash} onOpenChange={value => {if (!value) setConfirming(false);}} size="sm" role="alertdialog" title={t('next.ownerLibraries.emptyTrashTitle')}
      description={trash ? t('next.ownerLibraries.emptyTrashBody', {count: trash.total, library: trash.name}) : ''} footer={<>
        <Button disabled={busy} onClick={() => setConfirming(false)}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={busy || !trash || typed.trim() !== String(trash.total)} onClick={() => {void empty();}}>{t('next.ownerLibraries.emptyTrash')}</Button>
      </>}>
      <TextField label={trash ? t('next.ownerLibraries.emptyTrashType', {count: trash.total}) : ''} inputMode="numeric" value={typed} onChange={event => setTyped(event.currentTarget.value)} />
    </Dialog>
  </>;
}

const RULES: readonly Readonly<{value: TrashRule['mode']; label: MessageId}>[] = [
  {value: 'days', label: 'next.ownerLibraries.missingRuleDays'}, {value: 'scan', label: 'next.ownerLibraries.missingRuleScan'}, {value: 'manual', label: 'next.ownerLibraries.missingRuleManual'},
];

/** The library's own rule for emptying its trash (rule 60): the control of the settings row beside Missing titles. */
export function LibraryTrashRule({libraryId, api, isCurrent, label}: Readonly<{libraryId: string; api: ServerRequester; isCurrent: () => boolean; label: string}>) {
  const [trash, setTrash] = useState<Trash>(), [days, setDays] = useState(''), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const lifetime = useRef<AbortController | undefined>(undefined);
  const live = (capture: AbortController | undefined): capture is AbortController => !!capture && lifetime.current === capture && !capture.signal.aborted && isCurrent();
  const load = async (capture: AbortController) => {
    try { const next = await readLibraryTrash(api, libraryId, capture.signal); if (live(capture)) {setTrash(next); setDays(String(next.rule.days)); setFailed(false);} }
    catch { if (live(capture)) setFailed(true); }
  };
  const ports = useRef({load});
  useEffect(() => {ports.current = {load};});
  useEffect(() => {
    const capture = new AbortController(); lifetime.current = capture; void ports.current.load(capture);
    return () => {capture.abort(); if (lifetime.current === capture) lifetime.current = undefined;};
  }, [libraryId, api]);
  const [asking, setAsking] = useState<Readonly<{rule: TrashRule; count: number}>>();
  // Discourage, don't block: a rule that removes titles at the next scan, with no count typed, says how many first.
  const choose = (rule: TrashRule) => {
    if (!trash || busy) return;
    const count = removedAtNextScan(trash, rule);
    if (count > removedAtNextScan(trash, trash.rule)) setAsking({rule, count}); else void save(rule);
  };
  const save = async (rule: TrashRule) => {
    const capture = lifetime.current;
    setAsking(undefined);
    if (!live(capture) || busy || !trash) return;
    setBusy(true); setFailed(false);
    try { await saveTrashRule(api, libraryId, rule, capture.signal); if (live(capture)) await load(capture); }
    catch { if (live(capture)) setFailed(true); }
    finally { if (live(capture)) setBusy(false); }
  };
  const dayCount = Number(days), daysValid = /^\d+$/.test(days) && dayCount >= 1 && dayCount <= 3650;
  return <div className={styles.trashRule}>
    {failed && <span role="alert">{t('next.states.pageFailedBody')}</span>}
    {trash && <>
      <Select aria-label={label} value={trash.rule.mode} disabled={busy} onValueChange={mode => {
        if (mode === 'days' || mode === 'scan' || mode === 'manual') choose({mode, days: trash.rule.days});
      }} options={RULES.map(rule => ({value: rule.value, label: t(rule.label)}))} />
      {trash.rule.mode === 'days' && <>
        <TextField aria-label={t('next.ownerLibraries.missingRuleDaysLabel')} inputMode="numeric" value={days} disabled={busy} onChange={event => setDays(event.currentTarget.value)} />
        <Button disabled={busy || !daysValid || dayCount === trash.rule.days} onClick={() => choose({mode: 'days', days: dayCount})}>{t('next.settings.apply')}</Button>
      </>}
    </>}
    <Dialog open={!!asking && !!trash} onOpenChange={value => {if (!value) setAsking(undefined);}} size="sm" role="alertdialog"
      title={asking ? t('next.ownerLibraries.ruleRemovesTitle', {count: asking.count}) : ''}
      description={asking && trash ? t('next.ownerLibraries.ruleRemovesBody', {count: asking.count, library: trash.name}) : ''} footer={<>
        <Button disabled={busy} onClick={() => setAsking(undefined)}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" disabled={busy} onClick={() => {if (asking) void save(asking.rule);}}>{t('next.ownerLibraries.ruleRemovesConfirm')}</Button>
      </>}>{null}</Dialog>
  </div>;
}
