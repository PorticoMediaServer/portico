import {useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {Menu} from '../../../kit/overlays/Popover';
import {AddProfileDialog} from './AddProfileDialog';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';
const t = defaultI18n.t;

/** Actual account profiles only. The checked snapshot has no watch timestamps,
 * so the frame's illustrative "watched yesterday" is never supplied here. */
export function ProfileList({source, isCurrent, onFailure, onSelect}: {
  source: ProfileSettingsSource; isCurrent: () => boolean; onFailure: () => void; onSelect: () => void;
}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [adding, setAdding] = useState(false), [menu, setMenu] = useState<string>();
  const add = useRef<HTMLButtonElement>(null), document = state.document;
  if (!isCurrent() || !document) return null;
  const manage = document.canManage && document.capabilities.manageAccount;
  const move = async (id: string, direction: -1 | 1) => {
    if (!isCurrent() || state.busy) return;
    try {await source.move(id, direction);} catch {if (isCurrent() && !source.getSnapshot().error) onFailure();}
  };
  return <>
    <ul className={styles.profiles} aria-label={t('profiles.title')}>
      {document.profiles.map((profile, index) => <li key={profile.id}>
        <span className={styles.profileIcon}><Icon name="i-user" size={20} /></span>
        <span className={styles.profileName}><b>{profile.name}</b>{profile.id === state.inUseId
          ? <small>{t('profile.inUse')}</small>
          : (profile.primary || profile.pinRequired) && <small>{profile.primary ? t('profile.main') : t('profile.protected')}</small>}</span>
        <span className={styles.profileActions}>{manage && <>
          <Button size="xs" variant="glass" disabled={state.busy} onClick={() => {if (isCurrent()) {source.select(profile.id);onSelect();}}}>{t('next.settingsSpec.account.profile.groups.0.rows.0.control.itemAction.label')}</Button>
          <Menu title={profile.name} open={menu === profile.id} onOpenChange={open => setMenu(open ? profile.id : undefined)} items={[
            {id: 'up', label: t('web.profile.moveUp'), disabled: index === 0 || state.busy, onSelect: () => {void move(profile.id, -1);}},
            {id: 'down', label: t('web.profile.moveDown'), disabled: index === document.profiles.length - 1 || state.busy, onSelect: () => {void move(profile.id, 1);}},
          ]} trigger={<Button size="small" variant="icon" icon="i-more" iconOnly disabled={state.busy} aria-label={t('web.profile.options', {name: profile.name})} />} />
        </>}</span>
      </li>)}
    </ul>
    {manage && document.profiles.length < 8 && <Button ref={add} className={styles.addProfile} variant="glass" size="small" icon="i-plus" disabled={state.busy} onClick={() => {if (isCurrent()) setAdding(true);}}>{t('next.settingsSpec.account.profile.groups.0.rows.0.control.addLabel')}</Button>}
    {adding && <AddProfileDialog source={source} isCurrent={isCurrent} returnFocusRef={add} onClose={() => setAdding(false)} />}
  </>;
}
