import type {ReactNode} from 'react';
import {createI18n} from '@i18n';
import type {FactPart} from '@core/presentation/kit-model';
import {Icon, type IconName} from '../Icon';
import {IdentityName, ProfileAvatarFace, type ProfileIdentity} from './AccountSurfaces';
import type {ServerRole, ShellTranslate} from './ServerIdentity';
import type {ShellAction} from './SettingsSurfaces';
import styles from './statistics.module.css';

const tDefault = createI18n().t;
function Actions({actions}: {actions: readonly ShellAction[]}) { return <div className={styles.actions}>{actions.map(action => <button type="button" key={action.id} onClick={action.onAction} aria-label={action.iconOnly ? action.label : undefined} data-icon-only={action.iconOnly || undefined} data-variant={action.variant}>{action.icon && <Icon name={action.icon} size={action.iconOnly ? 20 : 16} />}{!action.iconOnly && action.label}</button>)}</div>; }
export function PersonRow({person, role, facts, activity, playing = false, disabled = false, self = false, actions, t = tDefault}: {
  person: ProfileIdentity; role: ServerRole; facts: readonly FactPart[]; activity?: string; playing?: boolean; disabled?: boolean; self?: boolean; actions: readonly ShellAction[]; t?: ShellTranslate;
}) {
  return <article className={styles.person} data-disabled={disabled || undefined}><ProfileAvatarFace initial={person.initial} art={person.art} size={48} appearance="session" /><div className={styles.personText}><h4><IdentityName person={person} />{role !== 'member' && <span className={styles.role} data-owner={role === 'owner' || undefined}>{t(role === 'owner' ? 'next.shell.owner' : 'next.shell.administrator')}</span>}{disabled && <span className={styles.role}>{t('next.shell.disabled')}</span>}{self && <span className={styles.self}>{t('next.shell.you')}</span>}</h4><div className={styles.personFacts}>{facts.map(fact => <span key={fact.id}>{fact.text}</span>)}</div></div><div className={styles.trailing}>{activity && <span className={styles.personActivity} data-playing={playing || undefined}>{activity}</span>}<Actions actions={actions} /></div></article>;
}
export function DeviceRow({name, icon, facts, profile, state, status, actions}: {name: string; icon: IconName; facts: readonly FactPart[]; profile?: ProfileIdentity; state: 'allowed' | 'pending' | 'blocked'; status?: string; actions: readonly ShellAction[]}) {
  return <article className={styles.device} data-state={state}><span className={styles.deviceIcon}><Icon name={icon} size={22} /></span><div><b>{name}</b><div className={styles.deviceFacts}>{profile && <><ProfileAvatarFace initial={profile.initial} art={profile.art} size={22} appearance="session" /><span><IdentityName person={profile} /></span></>}{facts.map(fact => <span key={fact.id}>{fact.text}</span>)}</div></div><div className={styles.trailing}>{status && <span className={styles.deviceStatus} data-state={state}>{status}</span>}<Actions actions={actions} /></div></article>;
}
export function WaitingNotice({children, action, icon = 'i-alert'}: {children: ReactNode; action?: ShellAction; icon?: IconName}) {
  return <div className={styles.waitingNotice}><Icon name={icon} size={20} /><div>{children}</div>{action && <button type="button" onClick={action.onAction}>{action.label}</button>}</div>;
}
