import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {Button} from '../controls';
import {Dialog} from '../overlays/Dialog';
import {ProfileAvatarFace, type ProfileTone} from './AccountSurfaces';
import type {ShellTranslate} from './ServerIdentity';
import styles from './switch-profile-server.module.css';

const tDefault = defaultI18n.t;

/** One profile the caller has already admitted for this dialog. The presenter
 * neither selects nor inspects profiles; `current` is a supplied fact. */
export type SwitchProfileServerProfile = Readonly<{
  id: string; name: string; initial: string; art?: ProfileTone; pinRequired?: boolean; current: boolean;
}>;

/** One server entry. `available` is the caller's admission, never inferred from
 * the status line or a dimmed style; detail text is already composed. */
export type SwitchProfileServerEntry = Readonly<{
  id: string; name: string; detail?: string; detailTone?: 'online'; current?: boolean; dimmed?: boolean; available: boolean;
}>;

export type SwitchProfileServerDialogProps = Readonly<{
  open: boolean; onClose: () => void;
  /** The drawn "Profiles on {server}" line, composed by the caller. */
  profilesLabel: string;
  profiles: readonly SwitchProfileServerProfile[];
  serversLabel?: string;
  servers?: readonly SwitchProfileServerEntry[];
  localLabel?: string;
  localServers?: readonly SwitchProfileServerEntry[];
  onSelectProfile?: (profile: SwitchProfileServerProfile) => void;
  onSelectServer?: (server: SwitchProfileServerEntry) => void;
  onRefreshServers?: () => void;
  onUseLocalServer?: () => void;
  /** Only a caller's real disabled/busy state; no local spinner of our own. */
  error?: ReactNode;
  disabled?: boolean;
  busy?: boolean;
  refreshing?: boolean;
  t?: ShellTranslate;
}>;

/** auth.html 22: switching profile or server from the avatar menu, desktop only.
 * The frame draws no phone variant, so this presenter is the authored desktop
 * surface and its invocation stays with the caller. */
export function SwitchProfileServerDialog({open, onClose, profilesLabel, profiles, serversLabel, servers, localLabel, localServers,
  onSelectProfile, onSelectServer, onRefreshServers, onUseLocalServer, error, disabled = false, busy = false, refreshing = false, t = tDefault}: SwitchProfileServerDialogProps) {
  const locked = disabled || busy;
  const profileRow = (profile: SwitchProfileServerProfile) => {
    const face = <ProfileAvatarFace initial={profile.initial} art={profile.art} size={48} locked={profile.pinRequired} t={t} />;
    const copy = <><b>{profile.name}</b><small>{profile.current ? t('profile.current') : profile.pinRequired ? t('pin.entry') : '\u00a0'}</small></>;
    if (profile.current || !onSelectProfile) return <span key={profile.id} className={styles.profile} data-current={profile.current || undefined} aria-current={profile.current ? 'true' : undefined}>{face}{copy}</span>;
    return <button key={profile.id} type="button" className={styles.profile} disabled={locked} onClick={() => onSelectProfile(profile)}>{face}{copy}</button>;
  };
  const serverRow = (server: SwitchProfileServerEntry) => {
    const content = <><span className={styles.serverIcon}><Icon name="i-server" size={20} /></span>
      <span className={styles.serverText}><b>{server.name}</b>{server.detail && <small data-tone={server.detailTone}>{server.detail}</small>}</span>
      {!server.current && <Icon name="i-right" size={20} />}</>;
    const className = styles.server;
    const facts = {'data-current': server.current || undefined, 'data-dimmed': server.dimmed || undefined};
    if (server.current || server.available === false || !onSelectServer) return <div key={server.id} className={className} {...facts} aria-current={server.current ? 'true' : undefined}>{content}</div>;
    return <button key={server.id} type="button" className={className} {...facts} disabled={locked} onClick={() => onSelectServer(server)}>{content}</button>;
  };
  const hasActions = Boolean(onRefreshServers || onUseLocalServer);
  return <Dialog open={open} onOpenChange={value => {if (!value) onClose();}} title={t('web.account.switchProfileOrServer')}
    responsive={false} width={600} className={styles.dialog} headerClassName={styles.header} bodyClassName={styles.body}>
    {error}
    <p className={styles.groupTitle} data-first>{profilesLabel}</p>
    <div className={styles.profiles}>{profiles.map(profileRow)}</div>
    {servers?.length ? <><p className={styles.groupTitle}>{serversLabel ?? t('web.servers.accountServers')}</p><div>{servers.map(serverRow)}</div></> : null}
    {localServers?.length ? <><p className={styles.groupTitle}>{localLabel ?? t('accounts.servers.local')}</p><div>{localServers.map(serverRow)}</div></> : null}
    {hasActions && <div className={styles.actions}>
      {onRefreshServers && <Button variant="ghost" size="small" icon="i-refresh" disabled={locked || refreshing} loading={busy || refreshing} onClick={onRefreshServers}>{t('accounts.servers.refresh')}</Button>}
      {onUseLocalServer && <Button variant="glass" size="small" disabled={locked} loading={busy} onClick={onUseLocalServer}>{t('web.chooser.localServer')}</Button>}
    </div>}
  </Dialog>;
}
