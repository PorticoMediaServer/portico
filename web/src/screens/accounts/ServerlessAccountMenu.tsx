import {useState} from 'react';
import {Icon} from '../../kit/Icon';
import {Menu, MenuItem, MenuSeparator} from '../../kit/overlays/Popover';
import {ProfileAvatar, ProfileAvatarFace} from '../../kit/shell/AccountSurfaces';
import {ShellScrim} from '../../kit/shell/ShellScrim';
import {usePhone} from '../../kit/overlays/shared';
import type {ShellTranslate} from '../../kit/shell/ServerIdentity';
import accountStyles from '../../kit/shell/account.module.css';
import styles from './serverless-account-menu.module.css';

/** connect30: an account without a server has no profile or server actions.
 * Session authority and navigation remain with the actual Services owner. */
export function ServerlessAccountMenu({username, name, t, onAccount, onJoin, onSetup, onApps, onHelp, onSignOut}: {
  username: string; name: string; t: ShellTranslate;
  onAccount: () => void; onJoin: () => void; onSetup: () => void;
  onApps: () => void; onHelp: () => void; onSignOut: () => void;
}) {
  const [open, setOpen] = useState(false), phone = usePhone();
  // Frame 30 lists Get the apps and draws the group dividers; the phone sheet of
  // frame 32c lists the five account entries without dividers, in one group.
  const divider = phone ? null : <MenuSeparator />;
  const initial = [...name][0]?.toLocaleUpperCase() ?? '';
  return <><ShellScrim open={open} context="account" />
    <Menu open={open} onOpenChange={setOpen} title={t('next.shell.account')}
      trigger={<ProfileAvatar initial={initial} name={name} />} width={350}
      sideOffset={17} alignOffset={-24} modal className={accountStyles.accountMenu}>
      <div className={`${accountStyles.who} ${styles.who}`}><ProfileAvatarFace initial={initial} size={48} />
        <div><b>{username}</b><small>{t('accounts.session.portico')}</small></div>
      </div>
      {divider}
      <MenuItem className={accountStyles.menuAction} icon="i-person" onSelect={onAccount}>
        <span>{t('services.workspace.title')}<small className={styles.description}>{t('services.start.accountMenuDescription')}</small></span>
      </MenuItem>
      <MenuItem className={accountStyles.menuAction} icon="i-link" onSelect={onJoin}>{t('services.start.join')}</MenuItem>
      <MenuItem className={accountStyles.menuAction} icon="i-server" onSelect={onSetup}>{t('services.start.setup')}</MenuItem>
      {!phone && <MenuItem className={accountStyles.menuAction} icon="i-phone" onSelect={onApps}>{t('services.start.apps')}</MenuItem>}
      {divider}
      <MenuItem className={accountStyles.menuAction} icon="i-question" onSelect={onHelp}>
        <span className={styles.help}>{t('web.shell.help')}<Icon name="i-ext" size={16} /></span>
      </MenuItem>
      {divider}
      <MenuItem className={accountStyles.menuAction} icon="i-signout" onSelect={onSignOut}>{t('action.signOut')}</MenuItem>
    </Menu>
  </>;
}
