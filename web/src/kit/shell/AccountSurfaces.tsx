import {forwardRef, type ButtonHTMLAttributes, type CSSProperties, type ReactElement, type ReactNode} from 'react';
import {createI18n} from '@i18n';
import type {FactPart} from '@core/presentation/kit-model';
import {Icon, type IconName} from '../Icon';
import {Artwork} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {Menu, MenuItem, MenuSeparator,MenuGroup} from '../overlays/Popover';
import type {OverlayChange} from '../overlays/types';
import type {ShellTranslate} from './ServerIdentity';
import {ShellScrim} from './ShellScrim';
import {differingProfileName} from './model';
import styles from './account.module.css';

const tDefault = createI18n().t;
export type ProfileTone = 'blue' | 'violet' | 'mint' | 'coral' | 'gold' | 'slate' | 'rose' | 'sky';
export type ProfileAvatarSize = 22 | 30 | 32 | 34 | 38 | 40 | 48 | 54 | 84 | 168;
export type ProfileIdentity = Readonly<{id: string; name: string; username?: string; initial: string; art?: ProfileTone; pinRequired?: boolean; primary?: boolean; revision?: number; pinRevision?: number; trustRevision?: number}>;
/** A profile label is never promoted into a username. */
export function IdentityName({person}: {person: Pick<ProfileIdentity, 'name' | 'username'>}) {
  const profile = differingProfileName(person.name, person.username);
  return <>{person.username || person.name}{person.username && profile && <span className={styles.identityProfile}>{profile}</span>}</>;
}
export type ProfileAvatarProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> & {
  initial: string; name: string; art?: ProfileTone; size?: ProfileAvatarSize; appearance?: 'profile' | 'session';
  current?: boolean; locked?: boolean; notificationDot?: boolean; source?: ArtworkSource; t?: ShellTranslate;
};
export const ProfileAvatar = forwardRef<HTMLButtonElement, ProfileAvatarProps>(function ProfileAvatar({initial, name, art = 'blue', size = 34, appearance = 'profile', current = false, locked = false, notificationDot = false, source, t = tDefault, className, ...props}, ref) {
  return <button {...props} ref={ref} type="button" className={[styles.avatarButton, className].filter(Boolean).join(' ')} aria-label={props['aria-label'] ?? name} aria-current={current || undefined} data-current={current || undefined}>
    <ProfileAvatarFace initial={initial} art={art} size={size} appearance={appearance} locked={locked} notificationDot={notificationDot} source={source} t={t} />
  </button>;
});

export function ProfileAvatarFace({initial, art = 'blue', size = 34, appearance = 'profile', locked = false, notificationDot = false, source, t = tDefault}: Pick<ProfileAvatarProps, 'initial' | 'art' | 'size' | 'appearance' | 'locked' | 'notificationDot' | 'source' | 't'>) {
  return <span className={styles.avatar} data-tone={art} data-size={size} data-appearance={appearance}>
    {source ? <Artwork source={source} alt="" width="100%" height="100%" fallback={initial} /> : initial}
    {locked && <span className={styles.lock} role="img" aria-label={t('next.shell.protected')}><Icon name="i-lock" size={14} /></span>}
    {notificationDot && <span className={styles.notificationDot} aria-hidden="true" />}
  </span>;
}

export type AccountMenuActions = Readonly<{
  manageProfiles?: () => void; addProfile?: () => void; switchProfile: (profile: ProfileIdentity) => void;
  switchServer: () => void; switchToLocalSignIn?: () => void; settings: () => void; notifications?: () => void; watchTogether: () => void; downloads?: () => void;
  feedback?: () => void; help: () => void; signOut: () => void; signOutPorticoAccount?: () => void;
}>;
export type AccountMenuProps = Readonly<{
  open: boolean; onOpenChange: OverlayChange; trigger: ReactElement; current: ProfileIdentity;
  primary: boolean; accountFacts: readonly FactPart[]; profiles: readonly ProfileIdentity[];
  serverChoices?:readonly {id:string;label:string;onSelect:()=>void}[]; actions: AccountMenuActions; signOutLabel?:string; serverAction?: ReactNode; notificationCount?: number; notificationDot?: boolean; t?: ShellTranslate;
}>;
export function AccountMenu({open, onOpenChange, trigger, current, primary, accountFacts, profiles, actions, serverChoices, signOutLabel, serverAction, notificationCount, notificationDot = false, t = tDefault}: AccountMenuProps) {
  const choose = (profile: ProfileIdentity) => actions.switchProfile(profile);
  return <><ShellScrim open={open} context="account" /><Menu open={open} onOpenChange={onOpenChange} trigger={trigger} title={t('next.shell.profileMenu', {name: current.name})} width={340} sideOffset={17} alignOffset={-24} modal className={styles.accountMenu}>
    <div className={styles.who}><ProfileAvatarFace initial={current.initial} art={current.art} size={48} /><div><b><IdentityName person={current} /></b>{primary && <small>{t('next.shell.mainProfile')}</small>}<small className={styles.factParts}>{accountFacts.map(fact => <span key={fact.id}>{fact.text}</span>)}</small></div></div>
    <div className={styles.switchProfiles}><div className={styles.switchLabel}>{t('next.shell.switchTo')}{actions.manageProfiles && <MenuItem className={styles.manageProfiles} onSelect={actions.manageProfiles}>{t('next.shell.manage')}</MenuItem>}</div>
      <div className={styles.profileRow}>{profiles.filter(profile => profile.id !== current.id).map(profile => <MenuItem key={profile.id} className={styles.miniProfile} onSelect={() => choose(profile)} aria-label={profile.primary ? t('next.shell.switchToMainProfile', {name: profile.name}) : undefined}><ProfileAvatarFace initial={profile.initial} art={profile.art} size={48} locked={profile.pinRequired} t={t} /><span>{profile.name}</span></MenuItem>)}
        {actions.addProfile && <MenuItem className={styles.addProfile} onSelect={actions.addProfile}><span className={styles.addFace}><Icon name="i-plus" size={22} /></span>{t('next.shell.add')}</MenuItem>}
      </div>
    </div>
    {serverAction}
    <MenuSeparator />
    {serverChoices?.length ? <MenuGroup label={t('next.shell.switchServerMenu')}>{serverChoices.map(server=><MenuItem key={server.id} className={styles.menuAction} icon="i-server" onSelect={server.onSelect}>{server.label}</MenuItem>)}</MenuGroup> : <MenuItem className={styles.menuAction} icon="i-server" onSelect={actions.switchServer}>{t('next.shell.switchServerMenu')}</MenuItem>}
    {actions.switchToLocalSignIn && <MenuItem className={styles.menuAction} icon="i-switch" onSelect={actions.switchToLocalSignIn}>{t('next.shell.switchToLocalSignIn')}</MenuItem>}
    <MenuItem className={styles.menuAction} icon="i-gear" onSelect={actions.settings}>{t('next.shell.settings')}</MenuItem>
    {actions.notifications && <MenuItem className={styles.menuAction} icon="i-bell" onSelect={actions.notifications} aria-label={notificationCount === undefined ? t('next.shell.notifications') : t('next.shell.notificationsUnread', {count: notificationCount})}><span className={styles.notificationAction}>{t('next.shell.notifications')}{notificationCount !== undefined && notificationCount > 0 ? <span className={styles.notificationCount}>{notificationCount}</span> : notificationDot ? <span className={styles.notificationMenuDot} aria-hidden="true" /> : undefined}</span></MenuItem>}
    <MenuSeparator />
    <MenuItem className={styles.menuAction} icon="i-together" onSelect={actions.watchTogether}>{t('next.shell.watchTogether')}</MenuItem>
    {actions.downloads && <MenuItem className={styles.menuAction} icon="i-download" onSelect={actions.downloads}>{t('next.shell.downloads')}</MenuItem>}
    {actions.feedback && <MenuItem className={styles.menuAction} icon="i-flag" onSelect={actions.feedback}>{t('next.shell.feedback')}</MenuItem>}
    <MenuItem className={styles.menuAction} icon="i-help" onSelect={actions.help}>{t('next.shell.help')}<Icon name="i-ext" size={16} className={styles.external} /></MenuItem>
    <MenuSeparator /><MenuItem className={styles.menuAction} icon="i-out" onSelect={actions.signOut}>{signOutLabel??t('next.shell.signOut')}</MenuItem>
    {actions.signOutPorticoAccount && <MenuItem className={styles.menuAction} icon="i-out" onSelect={actions.signOutPorticoAccount}>{t('accounts.signOut.portico')}</MenuItem>}
  </Menu></>;
}

export type NotificationSubject = Readonly<{kind: 'artwork'; source: ArtworkSource; position?: string}> | Readonly<{kind: 'device'; icon: IconName; warning?: boolean}> | Readonly<{kind: 'channel'; monogram: string; tint: string}>;
export type NotificationItemProps = Readonly<{
  title: string; body: string; when: readonly FactPart[]; unread: boolean; archived?: boolean;
  /** Missing typed subject facts omit the thumbnail; an item id alone is not artwork. */
  subject?: NotificationSubject; actions: readonly {id: string; label: string; disabled?: boolean; onAction: () => void}[];
  onMarkRead: () => void; onArchive: () => void; onMoveToInbox: () => void; layout?: 'desktop' | 'phone'; t?: ShellTranslate;
}>;
export function NotificationItem({title, body, when, unread, archived = false, subject, actions, onMarkRead, onArchive, onMoveToInbox, layout = 'desktop', t = tDefault}: NotificationItemProps) {
  return <article className={styles.notice} data-unread={unread || undefined} data-layout={layout} data-no-subject={!subject || undefined}>
    {subject && <span className={styles.subject} data-kind={subject.kind} data-warning={subject.kind === 'device' && subject.warning || undefined} style={subject.kind === 'channel' ? {'--subject-tint': subject.tint} as CSSProperties : undefined}>
      {subject.kind === 'artwork' ? <Artwork source={subject.source} alt="" position={subject.position ?? 'center 20%'} width="100%" height="100%" /> : subject.kind === 'device' ? <Icon name={subject.icon} size={layout === 'phone' ? 18 : 24} /> : subject.monogram}
    </span>}
    <div><b>{title}</b><p>{body}</p><div className={styles.when}>{when.map(fact => <span key={fact.id}>{fact.text}</span>)}</div><div className={styles.noticeActions}>{actions.map(action => <button type="button" key={action.id} disabled={action.disabled} onClick={action.onAction}>{action.label}</button>)}</div></div>
    <div className={styles.noticeTools}>{unread && <button type="button" aria-label={t('next.shell.markRead')} onClick={onMarkRead}><Icon name="i-check" size={18} /></button>}<button type="button" aria-label={t(archived ? 'next.shell.moveToInbox' : 'next.shell.archive')} onClick={archived ? onMoveToInbox : onArchive}><Icon name={archived ? 'i-undo' : 'i-archive'} size={18} /></button></div>
  </article>;
}

export type TogetherParticipant = Readonly<{id: string; initial: string; colour: string}>;
export function WatchTogetherPill({name, participants, count, status, waitingFor, onOpen, open, size = 'web', compact = false, flush = false, t = tDefault}: {
  name: string; participants: readonly TogetherParticipant[]; count: number; status: 'in-step' | 'catching-up' | 'waiting'; waitingFor?: string;
  onOpen: () => void; open?: boolean; size?: 'web' | 'phone' | 'tv'; compact?: boolean; flush?: boolean; t?: ShellTranslate;
}) {
  const label = status === 'in-step' ? t('next.shell.inStep') : status === 'catching-up' ? t('next.shell.catchingUp') : t('next.shell.waitingFor', {name: waitingFor ?? ''});
  return <button type="button" className={styles.together} data-size={size} data-compact={compact || undefined} data-flush={flush || undefined} data-waiting={status !== 'in-step' || undefined} onClick={onOpen} aria-haspopup="dialog" aria-expanded={open} aria-label={t('next.shell.togetherLabel', {name, count, state: label})}>
    <span className={styles.faces} aria-hidden="true">{participants.slice(0, 3).map(person => <span key={person.id} style={{background: person.colour}}>{person.initial}</span>)}</span><span className={styles.togetherText}><b>{name}</b><small>{label}</small></span>
  </button>;
}
