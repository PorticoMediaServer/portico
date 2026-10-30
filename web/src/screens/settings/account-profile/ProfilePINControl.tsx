import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {ManagedProfile} from '@core/profile-management.ts';
import {AuthCodeBoxes} from '../../../kit/accounts/AuthStage';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';
const t = defaultI18n.t;
export type ProfilePINRequest = Readonly<{source: ProfileSettingsSource; target: ManagedProfile; remove: boolean; trigger: HTMLElement | null}>;

/** Manager set/change/remove only. Forgot PIN retains separate reauthentication. */
export function ProfilePINDialog({request: intent, isCurrent, onClose, onFailure}: {
  request: ProfilePINRequest; isCurrent: () => boolean; onClose: () => void; onFailure: () => void;
}) {
  const {source, target, remove} = intent;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [done, setDone] = useState(false), [pin, setPIN] = useState(''), [repeat, setRepeat] = useState(''), [mismatch, setMismatch] = useState(false);
  const request = useRef(false), live = useRef(true), trigger = useRef(intent.trigger);
  useEffect(() => {live.current = true;return () => {live.current = false;};}, []);
  const permitted = isCurrent() && state.document?.canManage === true && state.document.capabilities.manageAccount;
  const clear = () => {setPIN('');setRepeat('');setMismatch(false);};
  const close = () => {if (!request.current) {clear();onClose();}};
  const save = async () => {
    if (done || !permitted || request.current || state.busy) return;
    if (!remove && (!/^\d{4}$/.test(pin) || !/^\d{4}$/.test(repeat))) return;
    if (!remove && pin !== repeat) {setMismatch(true);return;}
    const value = remove ? '' : pin;
    request.current = true;clear();
    try {
      await source.setPIN(target, value);
      if (live.current && isCurrent()) {if (remove) onClose();else setDone(true);}
    } catch {
      if (live.current && isCurrent()) {onClose();if (!source.getSnapshot().error) onFailure();}
    } finally {request.current = false;}
  };
  if (!permitted) return null;
  return <Dialog open width={420} returnFocusRef={trigger} onOpenChange={value => {if (!value) close();}}
    dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy}
    title={t(done ? 'profile.pinChanged' : remove ? 'profile.removePinConfirm.title' : 'profile.pinTitle', {name: target.name})}
    description={t(done ? 'profile.pinSavedBody' : remove ? 'profile.removePinConfirm.body' : 'profile.pinChoose', {name: target.name})}
    footer={done ? <Button variant="primary" size="small" onClick={close}>{t('action.done')}</Button> : <>
      <Button size="small" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button>
      <Button variant={remove ? 'glass' : 'primary'} className={remove ? styles.danger : undefined} size="small" loading={state.busy}
        disabled={state.busy || !remove && (pin.length !== 4 || repeat.length !== 4)} onClick={() => {void save();}}>{t(remove ? 'profile.removePin' : 'action.save')}</Button>
    </>}>
    {!done && !remove && <form className={styles.pinFields} onSubmit={event => {event.preventDefault();void save();}}>
      <label>{t('web.profile.newPin')}<AuthCodeBoxes length={4} value={pin} label={t('web.profile.newPin')} disabled={state.busy} autoFocus onChange={value => {setPIN(value);setMismatch(false);}} onComplete={setPIN} /></label>
      <label>{t('web.profile.repeatPin')}<AuthCodeBoxes length={4} value={repeat} label={t('web.profile.repeatPin')} disabled={state.busy}
        error={mismatch ? t('profile.pinMismatch') : undefined} onChange={value => {setRepeat(value);setMismatch(false);}} onComplete={setRepeat} /></label>
    </form>}
  </Dialog>;
}
