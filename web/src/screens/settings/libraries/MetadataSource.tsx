import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {metadataAgentPath, metadataAgentChange, parseMetadataAgent, type LibraryMetadataAgent} from '@core/server-admin/metadata-agent.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './libraries.module.css';

/** Explicit per-library consent only; changing a source never starts a refresh. */
export function MetadataSource({libraryId, api, isCurrent}: Readonly<{
  libraryId: string; api: LibraryContentApi; isCurrent: () => boolean;
}>) {
  const [document, setDocument] = useState<LibraryMetadataAgent>();
  const [choice, setChoice] = useState<string>(), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false), [retry, setRetry] = useState(0);
  const lifetime = useRef<{request: AbortController; running: boolean} | undefined>(undefined);
  useEffect(() => {
    const capture = {request: new AbortController(), running: false}; lifetime.current = capture;
    setDocument(undefined); setChoice(undefined); setFailed(false); setBusy(true);
    void api.request<unknown>(metadataAgentPath(libraryId), 'GET', undefined, capture.request.signal).then(raw => {
      const next = parseMetadataAgent(raw);
      if (next.libraryId !== libraryId) throw new Error('library_changed');
      if (lifetime.current === capture && isCurrent()) setDocument(next);
    }).catch(() => {if (lifetime.current === capture && isCurrent()) setFailed(true);})
      .finally(() => {if (lifetime.current === capture && isCurrent()) setBusy(false);});
    return () => {capture.request.abort();if (lifetime.current === capture) lifetime.current = undefined;};
  }, [api, libraryId, isCurrent, retry]);
  const save = async () => {
    const capture = lifetime.current, before = document, selected = choice;
    if (!capture || capture.running || !isCurrent() || !before || !selected || selected === before.agent || !before.agents.some(agent => agent.id === selected)) return;
    capture.running = true; setBusy(true); setFailed(false);
    try {
      const raw = await api.request<unknown>(metadataAgentPath(libraryId), 'PUT', metadataAgentChange(before, selected), capture.request.signal);
      const next = parseMetadataAgent(raw);
      if (next.libraryId !== libraryId || next.libraryKind !== before.libraryKind || next.agent !== selected || next.revision <= before.revision) throw new Error('metadata_unconfirmed');
      if (lifetime.current === capture && isCurrent()) {setDocument(next);setChoice(undefined);}
    } catch {
      // An uncertain write or conflict requires an explicit genuine reread, never a blind PUT retry.
      if (lifetime.current === capture && isCurrent()) {setDocument(undefined);setChoice(undefined);setFailed(true);}
    } finally {if (lifetime.current === capture && isCurrent()) {capture.running = false;setBusy(false);}}
  };
  return <section className={styles.form} aria-label={defaultI18n.t('next.ownerLibraries.metadataSource')}>
    <h2>{defaultI18n.t('next.ownerLibraries.metadataSource')}</h2>
    {document && <div role="radiogroup" aria-label={defaultI18n.t('next.ownerLibraries.metadataSource')}>
      {document.agents.map(agent => <label key={agent.id} className={styles.formNote}>
        <input type="radio" name={`metadata-source-${libraryId}`} checked={document.agent === agent.id} disabled={busy} onChange={() => {if (!lifetime.current?.running && isCurrent()) setChoice(agent.id);}} />
        <b>{agent.name}</b><p>{agent.description}</p>
      </label>)}
    </div>}
    <p className={styles.formNote}>{defaultI18n.t('next.ownerLibraries.metadataHelp')}</p>
    {busy && !document && <p role="status">{defaultI18n.t('next.settings.loading')}</p>}
    {failed && <div role="alert"><p>{defaultI18n.t('next.ownerLibraries.saveFailed')}</p><Button disabled={busy} onClick={() => setRetry(value => value + 1)}>{defaultI18n.t('next.states.tryAgain')}</Button></div>}
    <Dialog open={!!choice && choice !== document?.agent} onOpenChange={open => {if (!open && !lifetime.current?.running) setChoice(undefined);}}
      size="sm" title={defaultI18n.t('next.ownerLibraries.metadataSource')} description={defaultI18n.t('next.ownerLibraries.metadataHelp')}
      footer={<><Button disabled={busy} onClick={() => {if (!lifetime.current?.running) setChoice(undefined);}}>{defaultI18n.t('next.settings.cancel')}</Button>
        <Button variant="primary" loading={busy} disabled={busy} onClick={() => {void save();}}>{defaultI18n.t('next.ownerLibraries.save')}</Button></>}>
      <p>{document?.agents.find(agent => agent.id === choice)?.name}</p>
    </Dialog>
  </section>;
}
