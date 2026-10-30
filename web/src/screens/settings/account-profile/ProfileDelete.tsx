import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {ProfileDeletion, ProfileRetirementState} from '@core/profile-management.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';
const t = defaultI18n.t;

/** Read the producer's deletion preview before offering the destructive action.
 * The checked delete client sends that preview revision and lifetime, never a guessed one.
 * The dialog keeps saying Removing… while the accepted purge runs and Removed once the
 * server reports it; a blocked or failed retirement is left to the screen's error handling. */
export function ProfileDelete({source, isCurrent, onFailure}: {
  source: ProfileSettingsSource; isCurrent: () => boolean; onFailure: () => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [preview, setPreview] = useState<ProfileDeletion>(), [loading, setLoading] = useState(false), [retirement, setRetirement] = useState<ProfileRetirementState>();
  const pending = useRef(false), live = useRef(true), trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {live.current = true;return () => {live.current = false;};}, []);
  const selected = source.selected();
  const current = () => live.current && isCurrent() && !!source.getSnapshot().document;
  const close = () => {if (!pending.current) {setPreview(undefined);setRetirement(undefined);}};
  const open = async () => {
    if (!selected || pending.current || state.busy || !current()) return;
    pending.current = true;setLoading(true);
    try {const result = await source.previewDelete(selected);if (current()) setPreview(result);}
    catch {if (current()) onFailure();}
    finally {pending.current = false;if (current()) setLoading(false);}
  };
  const remove = async () => {
    if (!preview || retirement === 'removed' || pending.current || state.busy || !current()) return;
    pending.current = true;
    try {
      await source.deleteProfile(preview, status => {if (live.current && isCurrent()) setRetirement(status.state);});
    } catch {if (current() && !source.getSnapshot().error) onFailure();}
    finally {pending.current = false;}
  };
  const removed = retirement === 'removed', manageable = !!state.document?.canManage && state.document.capabilities.manageAccount === true;
  if (!isCurrent()) return null;
  // The button and a first confirmation need the manager's current document. An accepted
  // retirement keeps its own continuation: it only names the profile, revision and lifetime
  // it was accepted for, and every write still passes the manager gate in the source.
  const continuing = !!preview && retirement !== undefined;
  if (!continuing && (!manageable || !selected || selected.primary)) return null;
  const canRetry = manageable && !!selected;
  return <>
    <Button ref={trigger} className={styles.danger} size="small" variant="glass" loading={loading} disabled={state.busy || !manageable} onClick={() => {void open();}}>{t('next.settingsSpec.account.profile.groups.1.rows.3.control.label')}</Button>
    {preview && <Dialog open width={420} returnFocusRef={trigger} title={removed ? t('profile.deleteConfirm.removed') : t('profile.deleteConfirm.title', {name: preview.name ?? selected?.name ?? ''})}
      description={removed ? undefined : t('next.settingsSpec.account.profile.groups.1.rows.3.control.confirm')}
      dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy} onOpenChange={open => {if (!open) close();}}
      footer={removed
        ? <Button size="small" onClick={close}>{t('action.done')}</Button>
        : <><Button size="small" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button>
          <Button className={styles.danger} size="small" variant="glass" loading={state.busy} disabled={!canRetry} onClick={() => {void remove();}}>{t('profile.deleteConfirm.action')}</Button></>}>
      {!removed && retirement !== undefined && <p className={styles.limit} aria-live="polite">{t('profile.deleteConfirm.removing')}</p>}
      {!removed && typeof preview.playlists === 'number' && <p className={styles.limit}>{t('profile.deleteConfirm.playlists', {count: preview.playlists})}</p>}
    </Dialog>}
  </>;
}
