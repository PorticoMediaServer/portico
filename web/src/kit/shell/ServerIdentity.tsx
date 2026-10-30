import type {HTMLAttributes, ReactNode} from 'react';
import {createI18n, type I18n} from '@i18n';
import type {FactPart} from '@core/presentation/kit-model';
import {Icon} from '../Icon';
import styles from './identity.module.css';

export type ServerRole = 'owner' | 'administrator' | 'member';
export type ServerAudience = 'owner' | 'administrators';
export type ShellTranslate = I18n['t'];
const defaultTranslate = createI18n().t;

/** The mark carries no authority; the caller supplies the authorized area's facts. */
export type ServerMarkProps = Omit<HTMLAttributes<HTMLSpanElement>, 'children'> & ({size?: 22 | 24 | 26 | 34 | 38; context?: 'standard' | 'editor'} | {size: 36; context: 'file-info-phone'});
export function ServerMark({size = 26, context = 'standard', className, ...props}: ServerMarkProps) {
  const iconSize = size === 36 ? 20 : size === 22 ? 13 : size === 24 || size === 26 ? 15 : size === 34 ? context === 'editor' ? 20 : 18 : 19;
  return <span {...props} className={[styles.mark, className].filter(Boolean).join(' ')} data-size={size} data-context={context} aria-hidden="true"><Icon name={context === 'file-info-phone' ? 'i-server-file-info-phone' : 'i-server'} size={iconSize} /></span>;
}

/** An editor's authority is supplied by admission; a role alone never grants editing. */
export type EditorAudience = Readonly<{authority: 'owner'; label?: never} | {authority: 'administrator' | 'editor'; label: string}>;
export type ServerAudienceTagProps = {
  audience: EditorAudience; layout?: 'desktop' | 'phone'; className?: string; t?: ShellTranslate;
};
/** This complete treatment owns the one mark; callers must not prepend another. */
export function ServerAudienceTag({audience, layout = 'desktop', className, t = defaultTranslate}: ServerAudienceTagProps) {
  const label = audience.authority === 'owner' ? t(layout === 'phone' ? 'next.shell.editorAudience.ownerPhone' : 'next.shell.editorAudience.owner') : audience.label;
  return <span className={[styles.audienceTag, className].filter(Boolean).join(' ')} data-layout={layout} data-authority={audience.authority}><ServerMark size={layout === 'phone' ? 24 : 34} context="editor" /><span className={styles.audiencePill}>{t('next.shell.server')}<span>{label}</span></span></span>;
}

export type EditorHeaderProps = ServerAudienceTagProps & {
  title: string; description?: ReactNode; onClose: () => void; children?: ReactNode; titleId?: string;
};
/** The editor's title, description and optional real target artwork remain caller data. */
export function EditorHeader({audience, layout = 'desktop', title, description, onClose, children, titleId, className, t = defaultTranslate}: EditorHeaderProps) {
  return <header className={[styles.editorHeader, className].filter(Boolean).join(' ')} data-layout={layout}><div className={styles.editorTop}><ServerAudienceTag audience={audience} layout={layout} t={t} /><button type="button" onClick={onClose} aria-label={t('next.shell.close')}><Icon name="i-x" size={layout === 'phone' ? 18 : 20} /></button></div><h2 id={titleId}>{title}</h2>{description !== undefined && <p>{description}</p>}{children}</header>;
}

/** A restriction badge, not the viewer's role. Safe on a member's denied-state page. */
export type OwnerTagProps = ({variant?: 'badge'; identity?: never; size?: 'standard' | 'phone'} | {variant: 'state'; identity: ReactNode; size?: never}) & {className?: string; t?: ShellTranslate};
export function OwnerTag({identity, variant = 'badge', size = 'standard', className, t = defaultTranslate}: OwnerTagProps) {
  if (variant === 'badge') return <span className={[styles.badge, className].filter(Boolean).join(' ')} data-size={size}>{t('next.shell.ownerOnly')}</span>;
  return <span className={[styles.ownerState, className].filter(Boolean).join(' ')}><ServerMark />{identity}<span>{t('next.shell.ownerOnly')}</span></span>;
}


export type ServerHeaderProps = {
  serverName: string;
  health?: 'normal' | 'attention';
  facts?: readonly FactPart[];
  className?: string;
  t?: ShellTranslate;
} & ({viewerRole: 'owner'; audience: ServerAudience} | {viewerRole: 'administrator'; audience: 'administrators'});

/** Layout only: admission and the list of facts remain with the real settings model. */
export function ServerHeader({serverName, viewerRole, audience, health, facts = [], className, t = defaultTranslate}: ServerHeaderProps) {
  const visibility = viewerRole === 'administrator' ? 'next.shell.serverVisibility.administrator' : audience === 'owner' ? 'next.shell.serverVisibility.owner' : 'next.shell.serverVisibility.ownerShared';
  return <header className={[styles.header, className].filter(Boolean).join(' ')}>
    <div className={styles.serverPill}><Icon name="i-server" size={16} />{t('next.shell.server')}<span>{t(visibility)}</span></div>
    <div className={styles.nameLine}><b>{serverName}</b><div className={styles.facts}>
      {health && <span className={styles.health} data-health={health}>{t(health === 'normal' ? 'next.shell.health.normal' : 'next.shell.health.attention')}</span>}
      {facts.map(fact => <span key={fact.id} data-role={fact.role}>{fact.text}</span>)}
    </div></div>
  </header>;
}
