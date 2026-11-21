import {useEffect, useRef, useState} from 'react';
import type {ServerRequester} from '../../../../../../packages/client-core/src/query/server-api';
import {emptyLibraryTrash, readLibraryTrash, removedAtNextScan, saveTrashRule, type LibraryTrash, type TrashRule} from '../../../../../../packages/client-core/src/admin/library-trash';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';

type Translate = typeof defaultI18n.t;

export function useOwnerLibraryTrash({api, libraryId, parentCurrent, t}: {api: ServerRequester; libraryId: string; parentCurrent: () => boolean; t: Translate}) {
  const [trash, setTrash] = useState<LibraryTrash>(), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false), [days, setDays] = useState('');
  const [listOpen, setListOpen] = useState(false), [confirming, setConfirming] = useState(false), [typed, setTyped] = useState(''), [removed, setRemoved] = useState<number>();
  const [asking, setAsking] = useState<Readonly<{rule: TrashRule; count: number}>>();
  const lifetime = useRef<AbortController | undefined>(undefined);
  const live = (capture: AbortController | undefined): capture is AbortController => !!capture && lifetime.current === capture && !capture.signal.aborted && parentCurrent();
  const load = async (capture: AbortController) => {
    try {const next = await readLibraryTrash(api, libraryId, capture.signal); if (live(capture)) {setTrash(next); setDays(String(next.rule.days)); setFailed(false);}}
    catch {if (live(capture)) setFailed(true);}
  };
  const ports = useRef({load});
  useEffect(() => {ports.current = {load};});
  useEffect(() => {
    const capture = new AbortController(); lifetime.current = capture; setTrash(undefined); void ports.current.load(capture);
    return () => {capture.abort(); if (lifetime.current === capture) lifetime.current = undefined;};
  }, [api, libraryId]);
  const run = async (work: (capture: AbortController) => Promise<void>) => {
    const capture = lifetime.current;
    if (!live(capture) || busy) return;
    setBusy(true); setFailed(false);
    try {await work(capture);} catch {if (live(capture)) setFailed(true);} finally {if (live(capture)) setBusy(false);}
  };
  const save = (rule: TrashRule) => {setAsking(undefined); void run(async capture => {await saveTrashRule(api, libraryId, rule, capture.signal); if (live(capture)) await load(capture);});};
  const choose = (rule: TrashRule) => {
    if (!trash || busy) return;
    const count = removedAtNextScan(trash, rule);
    if (count > removedAtNextScan(trash, trash.rule)) setAsking({rule, count}); else save(rule);
  };
  const empty = () => void run(async capture => {
    const count = await emptyLibraryTrash(api, libraryId, typed.trim(), capture.signal);
    if (!live(capture)) return;
    setConfirming(false); setTyped(''); setRemoved(count); await load(capture);
  });
  const dayCount = Number(days), daysValid = /^\d+$/.test(days) && dayCount >= 1 && dayCount <= 3650;
  const removal = (removedAt?: string) => !trash || trash.rule.mode === 'manual' || !removedAt ? undefined
    : trash.rule.mode === 'scan' ? t('next.ownerLibraries.missingRemovedNextScan') : t('next.ownerLibraries.missingRemovedOn', {date: defaultI18n.date(removedAt)});
  const label = (key: string) => t(`next.settingsSpec.server.libraries.panels.library.tabs.3.groups.2.rows.${key}` as Parameters<Translate>[0]);
  return {trash, failed, busy, days, setDays, dayCount, daysValid, listOpen, setListOpen, confirming, setConfirming,
    typed, setTyped, removed, setRemoved, asking, setAsking, label, removal, choose, save, empty};
}
