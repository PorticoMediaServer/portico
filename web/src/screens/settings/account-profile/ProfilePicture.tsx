import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {AVATAR_MAX_BYTES} from '@core/identity-client.ts';
import type {ManagedProfile} from '@core/profile-management.ts';
import {Button} from '../../../kit/controls';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';
const t = defaultI18n.t;
export function ProfilePicture({source, isCurrent, onFailure}: {source: ProfileSettingsSource; isCurrent: () => boolean; onFailure: () => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const picker = useRef<HTMLInputElement>(null), target = useRef<ManagedProfile | undefined>(undefined), pending = useRef(false);
  const [error, setError] = useState<'size' | 'type'>();
  useEffect(() => {void source.loadPictures();}, [source]);
  const selected = source.selected();
  if (!isCurrent() || !state.document?.canManage || !selected || !state.avatars) return null;
  const run = async (profile: ManagedProfile, file?: File) => {
    if (pending.current || state.busy || !isCurrent()) return;
    if (file && (!file.size || file.size > AVATAR_MAX_BYTES)) {setError('size');return;}
    if (file && !['image/jpeg', 'image/png'].includes(file.type)) {setError('type');return;}
    pending.current = true;setError(undefined);
    try {await source.picture(profile, file);} catch {if (isCurrent() && !source.getSnapshot().error) onFailure();}
    finally {pending.current = false;}
  };
  return <div className={styles.pictureActions}>
    <input ref={picker} type="file" hidden accept="image/jpeg,image/png" onChange={event => {
      const file = event.currentTarget.files?.[0], captured = target.current;event.currentTarget.value = '';target.current = undefined;
      if (file && captured) void run(captured, file);
    }} />
    <Button size="small" variant="glass" disabled={state.busy} onClick={() => {if (isCurrent()) {target.current = selected;picker.current?.click();}}}>{t('next.settingsSpec.account.profile.groups.1.rows.2.control.label')}</Button>
    {state.avatars.some(avatar => avatar.profileId === selected.id) && <Button size="small" variant="glass" disabled={state.busy} onClick={() => {void run(selected);}}>{t('next.settingsSpec.account.profile.groups.1.rows.2.control.second.label')}</Button>}
    {error && <p role="alert" className={styles.pictureError}>{t(error === 'size' ? 'web.profile.pictureTooLarge' : 'profile.pictureRejected')}</p>}
  </div>;
}
