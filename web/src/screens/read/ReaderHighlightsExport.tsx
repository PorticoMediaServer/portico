import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {collectReadingHighlights} from '@core/reading-highlights-export.ts';
import type {ReadingAnnotationService} from '@core/reading-annotation-service.ts';
import {Button} from '../../kit/controls';

type Lease = {life: AbortController};
type Attempt = {owner: Lease; life: AbortController; url?: string};
/** Browser-only local file sink. Native hosts must supply a real save port first. */
export function ReaderHighlightsExport({service, signal, title, author, reportError, nativeSave}: {service: ReadingAnnotationService; signal: AbortSignal; title?: string; author?: string; reportError: (error: unknown) => void; nativeSave?: (revision: number, signal: AbortSignal) => Promise<'saved' | 'cancelled'>}) {
  const [lease, setLease] = useState<Lease>();
  const [running, setRunning] = useState<Attempt>();
  const request = useRef<Attempt | undefined>(undefined);
  const owner = useRef({service, signal}); owner.current = {service, signal};
  const retire = (attempt: Attempt) => {attempt.life.abort(); if (attempt.url) URL.revokeObjectURL(attempt.url); attempt.url = undefined; if (request.current === attempt) request.current = undefined;};
  useLayoutEffect(() => {
    const original: Lease = {life: new AbortController()}; setLease(original);
    const abort = () => {original.life.abort(); const attempt = request.current; if (attempt?.owner === original) retire(attempt);};
    signal.addEventListener('abort', abort, {once: true}); service.port.signal.addEventListener('abort', abort, {once: true});
    return () => {abort(); signal.removeEventListener('abort', abort); service.port.signal.removeEventListener('abort', abort);};
  }, [service, signal]);
  const start = () => {
    if (!lease || lease.life.signal.aborted || request.current || signal.aborted || !service.current() || owner.current.service !== service || owner.current.signal !== signal) return;
    const state = service.getSnapshot(), revision = state.revision;
    if (state.loading || state.saving || revision === undefined) return;
    const attempt: Attempt = {owner: lease, life: new AbortController()}; request.current = attempt;
    const current = () => request.current === attempt && !attempt.life.signal.aborted && !lease.life.signal.aborted && !signal.aborted && service.current()
      && owner.current.service === service && owner.current.signal === signal && service.getSnapshot().revision === revision && !service.getSnapshot().saving;
    setRunning(attempt);
    void (async () => {
      if (nativeSave) {await nativeSave(revision, AbortSignal.any([signal, lease.life.signal, attempt.life.signal])); return;}
      const file = await collectReadingHighlights({port: service.port, revision, title, author, signal: AbortSignal.any([signal, lease.life.signal, attempt.life.signal]), isCurrent: current});
      if (!current()) return;
      const blob = new Blob([file.markdown], {type: 'text/markdown;charset=utf-8'});
      if (!current()) return;
      attempt.url = URL.createObjectURL(blob);
      const link = document.createElement('a'); link.href = attempt.url; link.download = file.fileName; link.rel = 'noreferrer';
      try {if (current()) {document.body.append(link); link.click();}} finally {link.remove();}
    })().catch(error => {if (current()) reportError(error);}).finally(() => {const owned = request.current === attempt; retire(attempt); if (owned) setRunning(value => value === attempt ? undefined : value);});
  };
  return <Button disabled={!!running && running.owner === lease && !running.life.signal.aborted || !lease || lease.life.signal.aborted || signal.aborted || !service.current()} onClick={start}>{defaultI18n.t('next.reader.exportHighlights')}</Button>;
}
