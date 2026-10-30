import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './libraries.module.css';

/** The drawn explicit rename form, backed by the current library command. */
export function RenameLibrary({name, disabled, rename, request = 0, hideTrigger = false}: Readonly<{
  request?: number; hideTrigger?: boolean; name: string; disabled: boolean; rename: (name: string) => Promise<boolean>;
}>) {
  const [open, setOpen] = useState(false), [draft, setDraft] = useState(name);
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const lifetime = useRef<{running: boolean} | undefined>(undefined);
  useEffect(() => {
    const capture = {running: false}; lifetime.current = capture;
    return () => {if (lifetime.current === capture) lifetime.current = undefined;};
  }, []);
  const handledRequest = useRef(0);
  useEffect(() => {if (request > handledRequest.current) {handledRequest.current = request;setDraft(name);setFailed(false);setOpen(true);}}, [request, name]);
  const close = (next: boolean) => {if (!lifetime.current?.running) setOpen(next);};
  const save = async () => {
    const capture = lifetime.current;
    if (!capture || capture.running || disabled || !draft.trim()) return;
    capture.running = true; setBusy(true); setFailed(false);
    try {
      const saved = await rename(draft.trim());
      if (lifetime.current !== capture) return;
      if (saved) setOpen(false); else setFailed(true);
    } catch {if (lifetime.current === capture) setFailed(true);}
    finally {if (lifetime.current === capture) {capture.running = false;setBusy(false);}}
  };
  return <>
    {!hideTrigger && <><span>{name}</span>
    <Button variant="glass" size="small" disabled={disabled} onClick={() => {setDraft(name);setFailed(false);setOpen(true);}}>{defaultI18n.t('next.ownerLibraries.rename')}</Button></>}
    <Dialog open={open} onOpenChange={close} size="sm" title={defaultI18n.t('next.ownerLibraries.renameTitle')}
      footer={<><Button disabled={busy} onClick={() => close(false)}>{defaultI18n.t('next.settings.cancel')}</Button>
        <Button variant="primary" disabled={busy || disabled || !draft.trim()} loading={busy} onClick={() => {void save();}}>{defaultI18n.t('next.ownerLibraries.rename')}</Button></>}>
      <div className={styles.form}>
        <TextField label={defaultI18n.t('next.ownerLibraries.nameLabel')} value={draft} disabled={busy} onChange={event => setDraft(event.currentTarget.value)} />
        {failed && <p role="alert">{defaultI18n.t('next.ownerLibraries.saveFailed')}</p>}
      </div>
    </Dialog>
  </>;
}
