import {useEffect, useRef, useState, useSyncExternalStore, type CSSProperties, type RefObject} from 'react';
import {profileArtColors} from '@design';
import {defaultI18n} from '@i18n';
import {PROFILE_ART, type ProfileArt} from '@core/profile-management.ts';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';

const t = defaultI18n.t;

/** owner-people frame14: Name, eight colors, limit, Cancel and Save.
 * The source rechecks actual account admission at submission; opening this
 * surface never creates a profile or borrows another profile's permissions. */
export function AddProfileDialog({source, isCurrent, onClose, returnFocusRef}: {
  source: ProfileSettingsSource; isCurrent: () => boolean; onClose: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const snapshot = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const document = snapshot.document;
  const finished = useRef(false);
  useEffect(() => {
    if (finished.current || !isCurrent()) return;
    // The always-mounted list owns source errors; this child only dismisses.
    if (document && !snapshot.busy && (!document.canManage || !document.capabilities.manageAccount || document.profiles.length >= 8)) {
      finished.current = true;onClose();
    }
  }, [snapshot, document, isCurrent, onClose]);
  if (!isCurrent() || !document?.canManage || !document.capabilities.manageAccount || document.profiles.length >= 8) return null;
  return <AddProfileForm source={source} busy={snapshot.busy} isCurrent={isCurrent} onClose={() => {
    if (!finished.current) {finished.current = true;onClose();}
  }} returnFocusRef={returnFocusRef} />;
}

function AddProfileForm({source, busy, isCurrent, onClose, returnFocusRef}: {
  source: ProfileSettingsSource; busy: boolean; isCurrent: () => boolean; onClose: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const [name, setName] = useState(''), [art, setArt] = useState<ProfileArt>('blue');
  const input = useRef<HTMLInputElement>(null), submitting = useRef(false), closed = useRef(false);
  useEffect(() => {closed.current = false;return () => {closed.current = true;};}, []);
  const close = () => {closed.current = true;setName('');onClose();};
  const submit = async () => {
    if (!isCurrent() || closed.current || submitting.current || busy || !name.trim() || name.trim().length > 120) return;
    submitting.current = true;
    try {
      await source.create(name.trim(), art);
      if (!closed.current && isCurrent()) close();
    } catch {
      if (!closed.current && isCurrent()) close();
    } finally {submitting.current = false;}
  };
  return <Dialog open width={420} title={t('profile.addTitle')} initialFocusRef={input} returnFocusRef={returnFocusRef}
    dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy} onOpenChange={open => {if (!open && !busy) close();}}
    footer={<><Button size="small" disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={busy} disabled={busy || !name.trim()} onClick={() => {void submit();}}>{t('action.save')}</Button></>}>
    <form className={styles.fields} onSubmit={event => {event.preventDefault();void submit();}}>
      <TextField ref={input} label={t('profile.name')} value={name} maxLength={120} disabled={busy} autoComplete="off" onChange={event => setName(event.currentTarget.value)} />
      <fieldset className={styles.colors} disabled={busy}><legend>{t('web.profile.color')}</legend><div role="radiogroup" aria-label={t('web.profile.color')}>
        {PROFILE_ART.map(color => <button key={color} type="button" role="radio" aria-checked={art === color} aria-label={t('web.profile.colorName', {color})}
          tabIndex={art === color ? 0 : -1} onKeyDown={event => {
            const index = PROFILE_ART.indexOf(color);
            const next = event.key === 'Home' ? 0 : event.key === 'End' ? PROFILE_ART.length - 1 : ['ArrowRight', 'ArrowDown'].includes(event.key) ? (index + 1) % PROFILE_ART.length : ['ArrowLeft', 'ArrowUp'].includes(event.key) ? (index + PROFILE_ART.length - 1) % PROFILE_ART.length : undefined;
            if (next === undefined) return;event.preventDefault();setArt(PROFILE_ART[next]!);
            const buttons = event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]');buttons?.[next]?.focus();
          }} style={{'--profile-color': profileArtColors[color]} as CSSProperties} onClick={() => setArt(color)} disabled={busy} />)}
      </div></fieldset>
      <p className={styles.limit}>{t('profile.limitReached')}</p>
    </form>
  </Dialog>;
}
