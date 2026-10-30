import {createContext, useContext, useEffect, useState, type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import {inPageClick} from '../../platform/hosted-navigation';
import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {Icon, type IconName} from '../Icon';
import {ProfileAvatarFace} from '../shell/AccountSurfaces';
import {SettingsRow} from '../shell/SettingsSurfaces';
import {Button, TextField} from '../controls';
import {ColourBackdrop} from '../artwork';
import wordmark from '../../shell/wordmark.svg';
import styles from './services-workspace.module.css';

const t = createI18n(resolveRegion(undefined, deviceRegion())).t;
type WorkspaceIconName = IconName | 'i-file';
function WorkspaceIcon({name, size}: {name: WorkspaceIconName; size: number}) {
  // Exact account.html69f2d45 #i-file; the shared catalogue has no copy of this symbol.
  if (name === 'i-file') return <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false"><g fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M7 3.5h7l4 4V20a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1zM14 3.5V8h4" /></g></svg>;
  return <Icon name={name} size={size} />;
}
type QREncoder = typeof import('qrcode');
let qrEncoder: Promise<QREncoder> | undefined;
function AuthenticatorQR({value, onError}: {value: string; onError: (error: unknown) => void}) {
  const [cells, setCells] = useState<{value: string; size: number; path: string}>();
  useEffect(() => {
    let active = true;
    setCells(undefined);
    // This is a local encoder of the actual otpauth URI. No image service receives the secret.
    void (qrEncoder ??= import('qrcode')).then(encoder => {
      const code = encoder.create(value, {errorCorrectionLevel: 'M'}), path: string[] = [];
      for (let y = 0; y < code.modules.size; y++) for (let x = 0; x < code.modules.size; x++)
        if (code.modules.get(y, x)) path.push(`M${x} ${y}h1v1h-1z`);
      if (active) setCells({value, size: code.modules.size, path: path.join('')});
    }).catch(error => { if (active) onError(error); });
    return () => { active = false; };
  }, [value, onError]);
  return cells?.value === value ? <svg className={styles.authenticatorQR} viewBox={`-4 -4 ${cells.size + 8} ${cells.size + 8}`} role="img" aria-label={t('qr.label')} shapeRendering="crispEdges"><path d={cells.path} /></svg> : null;
}

export type ServicesSection = 'devices' | 'security' | 'servers' | 'notifications';
const sections: readonly {id: ServicesSection; icon: IconName; label: string}[] = [
  {id: 'devices', icon: 'i-laptop', label: t('services.workspace.devices')},
  {id: 'security', icon: 'i-shield', label: t('services.workspace.security')},
  {id: 'servers', icon: 'i-server', label: t('services.workspace.servers')},
  {id: 'notifications', icon: 'i-bell', label: t('services.workspace.notifications')},
];

/** No server/library controls exist in the Services header. */
export function ServicesHeader({avatar, unread = false, notifications = true}: {avatar: ReactNode; unread?: boolean; notifications?: boolean}) {
  return <header className={styles.header}>
    <a className={styles.brand} href="/" aria-label={t('next.platform.appName')} onClick={event => inPageClick(event.nativeEvent, '/')}><img src={wordmark} alt="" /></a>
    <a className={styles.home} href="/" onClick={event => inPageClick(event.nativeEvent, '/')}>{t('web.shell.home')}</a>
    <div className={styles.tools}>{notifications && <a className={styles.bell} href="/account?tab=notifications" aria-label={t('services.workspace.notifications')} onClick={event => inPageClick(event.nativeEvent, '/account?tab=notifications')}><Icon name="i-bell" size={22} />{unread && <span className={styles.unread} />}</a>}{avatar}</div>
  </header>;
}

/** account22: display the actual enrollment secret; copying keeps its original bytes. */
export function ServicesAuthenticator({secret, uri, onCopy, onError}: {secret: string; uri?: string; onCopy: () => void; onError: (error: unknown) => void}) {
  return <div className={styles.authenticator}>{uri && <AuthenticatorQR value={uri} onError={onError} />}<div>
    <div className={styles.keyLine}><code>{secret.match(/.{1,4}/g)?.join(' ') ?? secret}</code><button type="button" aria-label={t('web.twoStep.copy')} onClick={onCopy}><Icon name="i-copy" size={18} /></button></div>
    {uri && <a className={styles.authenticatorLink} href={uri}>{t('web.hostedSecurity.openAuthenticator')}</a>}
  </div></div>;
}

/** Keep every returned recovery code, in its original order and spelling. */
export function ServicesRecoveryCodes({codes, onCopy, onDownload}: {codes: readonly string[]; onCopy: () => void; onDownload: () => void}) {
  const rows = Math.ceil(codes.length / 2);
  return <><div className={styles.recoveryCodes} style={{gridTemplateRows: `repeat(${Math.max(1, rows)}, auto)`}}>
    {codes.map((code, index) => <span key={index} data-first-row={index === 0 || index === rows || undefined}><i>{index + 1}</i>{code}</span>)}
  </div><div className={styles.recoveryActions}><Button size="small" icon="i-copy" onClick={onCopy}>{t('web.twoStep.copy')}</Button><Button size="small" icon="i-download" onClick={onDownload}>{t('action.download')}</Button></div></>;
}

export function ServicesDangerAction({children, onClick, disabled = false, loading = false, compact = false, icon}: {children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean; compact?: boolean; icon?: IconName}) {
  return <Button className={styles.dangerAction} size={compact ? 'xs' : 'small'} icon={icon} onClick={onClick} disabled={disabled} loading={loading}>{children}</Button>;
}

/** Where a section's own heading and phone actions render (H14). The workspace stays mounted while
 * sections change; a section that owns its actions (Devices) places them here. */
const WorkspaceSlots = createContext<Readonly<{heading: HTMLElement | null; phone: HTMLElement | null}>>({heading: null, phone: null});
export function ServicesWorkspaceActions({children}: {children: ReactNode}) {
  const slots = useContext(WorkspaceSlots);
  return <>{slots.heading && createPortal(children, slots.heading)}{slots.phone && createPortal(children, slots.phone)}</>;
}
export function ServicesWorkspace({header, account, section, onSection, onSignOut, description, actions, phoneActions, unread = 0, children}: {
  header: ReactNode; account: {name: string; email?: string; initial: string}; section: ServicesSection;
  onSection: (section: ServicesSection) => void; onSignOut: () => void; description?: ReactNode; actions?: ReactNode; phoneActions?: ReactNode; unread?: number; children: ReactNode;
}) {
  const selected = sections.find(item => item.id === section)!;
  const [heading, setHeading] = useState<HTMLDivElement | null>(null), [phone, setPhone] = useState<HTMLDivElement | null>(null);
  return <WorkspaceSlots.Provider value={{heading, phone}}><div className={styles.workspace}>{header}<ColourBackdrop />
    <div className={styles.workspaceGrid}><aside className={styles.aside}>
      <h1>{t('services.workspace.title')}</h1><div className={styles.who}><ProfileAvatarFace initial={account.initial} size={48} /><span><b>{account.name}</b>{account.email && <small>{account.email}</small>}</span></div>
      <nav aria-label={t('services.workspace.sections')}>{sections.map(item => <button type="button" key={item.id} aria-current={item.id === section ? 'page' : undefined} onClick={() => onSection(item.id)}><Icon name={item.icon} size={19} />{item.label}</button>)}<div className={styles.navSpacer} /><button type="button" onClick={onSignOut}><Icon name="i-signout" size={19} />{t('action.signOut')}</button></nav>
    </aside><main className={styles.pane}>
      <span className={styles.pill}><Icon name="i-person" size={16} />{t('services.workspace.title')}<span>{t('services.workspace.subtitle')}</span></span>
      <div className={styles.heading}><h2>{selected.label}</h2><div ref={setHeading} className={styles.headingActions}>{actions}</div></div>{description && <p className={styles.lead}>{description}</p>}
      <nav className={styles.phoneTabs} aria-label={t('services.workspace.sections')}>{sections.map(item => <button key={item.id} type="button" aria-current={item.id === section ? 'page' : undefined} onClick={() => onSection(item.id)}>{item.label}</button>)}</nav>
      {children}
      <div ref={setPhone} className={styles.phoneActions}>{phoneActions}</div>
    </main></div>
  </div></WorkspaceSlots.Provider>;
}

export function ServicesGroup({title, description, actions, danger = false, children}: {title?: ReactNode; description?: ReactNode; actions?: ReactNode; danger?: boolean; children: ReactNode}) {
  return <section className={styles.group}>{(title || actions) && <div className={styles.groupHeading}>{title && <h3>{title}</h3>}{actions && <div>{actions}</div>}</div>}{description && <p className={styles.groupDescription}>{description}</p>}<div className={styles.groupBox} data-danger={danger || undefined}>{children}</div></section>;
}
export function ServicesWorkspaceRow({title, help, helpPhone, children, icon, phoneIcon, phoneTone, badge, dim}: {title: ReactNode; help?: ReactNode; helpPhone?: ReactNode; children?: ReactNode; icon?: IconName; phoneIcon?: WorkspaceIconName; phoneTone?: 'green'; badge?: ReactNode; dim?: boolean}) {
  if (!icon && !phoneIcon) return <SettingsRow className={styles.plainRow} label={<>{title}{badge}</>} help={help}>{children}</SettingsRow>;
  return <div className={styles.iconRow} data-phone-icon-only={!icon || undefined} data-dim={dim || undefined}><span className={styles.icon} data-icon={icon ?? phoneIcon} data-phone-tone={phoneTone}><WorkspaceIcon name={(icon ?? phoneIcon)!} size={22} /></span><div><b>{title}{badge}</b>{help && <p>{helpPhone === undefined ? help : <><span className={styles.factDesktop}>{help}</span><span className={styles.factPhone}>{helpPhone}</span></>}</p>}</div><div>{children}</div></div>;
}
/** A notice or note that follows a group: the standard gap above it (H14). */
export function ServicesGroupNote({children}: {children: ReactNode}) { return <div className={styles.groupNote}>{children}</div>; }
export function ServicesRowNote({children}: {children: ReactNode}) { return <p className={styles.rowNote}>{children}</p>; }
export function ServicesInlineFacts({items}: {items: readonly string[]}) { return <span className={styles.inlineFacts}>{items.filter(Boolean).map((item, index) => <span key={index}>{item}</span>)}</span>; }
export function ServicesDeviceFacts({desktop, phone}: {desktop: readonly string[]; phone: readonly string[]}) {
  return <><span className={styles.factDesktop}><ServicesInlineFacts items={desktop} /></span><span className={styles.factPhone}><ServicesInlineFacts items={phone} /></span></>;
}
export function ServicesBadge({children, tone = 'default'}: {children: ReactNode; tone?: 'default' | 'accent' | 'healthy'}) { return <span className={styles.badge} data-tone={tone}>{children}</span>; }
export function ServicesEmpty({title, description, actions}: {title: ReactNode; description: ReactNode; actions?: ReactNode}) { return <div className={styles.empty}><b>{title}</b><p>{description}</p>{actions && <div className={styles.emptyActions}>{actions}</div>}</div>; }
export function ServicesFeedRow({unread, icon, tone = 'blue', title, body, when, actions}: {unread: boolean; icon: IconName; tone?: 'blue' | 'green' | 'amber'; title: ReactNode; body: ReactNode; when: ReactNode; actions?: ReactNode}) {
  return <article className={styles.feedRow} data-unread={unread || undefined}><span className={styles.icon} data-tone={tone}><Icon name={icon} size={24} /></span><div><b>{title}</b><p>{body}</p>{actions && <div className={styles.feedActions}>{actions}</div>}</div><time>{when}</time></article>;
}

const downloads = [
  {id: 'windows', title: 'services.start.windows', detail: 'services.start.windowsDetail'},
  {id: 'macos', title: 'services.start.macos', detail: 'services.start.macosDetail'},
  {id: 'linux', title: 'services.start.linux', detail: 'services.start.linuxDetail'},
  {id: 'docker', title: 'services.start.docker', detail: 'services.start.dockerDetail'},
] as const;
const apps = [{icon: 'i-apple', title: 'services.start.iphone'}, {icon: 'i-phone', title: 'services.start.android'}, {icon: 'i-tv', title: 'services.platform.tvos'}, {icon: 'i-tvlib', title: 'services.start.tv'}] as const;
const steps = [{title: 'services.start.install', body: 'services.start.installBody'}, {title: 'services.start.name', body: 'services.start.nameBody'}, {title: 'services.start.library', body: 'services.start.libraryBody'}] as const;

/** Exact connect.html69f2d45 symbols, kept inside this owned kit module. */
function DownloadIcon({kind}: {kind: (typeof downloads)[number]['id']}) {
  if (kind === 'macos') return <Icon name="i-apple" size={26} />;
  return <svg viewBox="0 0 24 24" width={26} height={26} aria-hidden="true" focusable="false">
    {kind === 'windows' ? <g fill="currentColor"><rect x="3.5" y="3.5" width="7.8" height="7.8" rx="1.2" /><rect x="12.7" y="3.5" width="7.8" height="7.8" rx="1.2" /><rect x="3.5" y="12.7" width="7.8" height="7.8" rx="1.2" /><rect x="12.7" y="12.7" width="7.8" height="7.8" rx="1.2" /></g>
      : kind === 'linux' ? <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 3 3-3 3M12.5 15H17" /></g>
      : <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round"><path d="M12 3 20 7.5v9L12 21l-8-4.5v-9z" /><path d="M4.4 7.7 12 12l7.6-4.3M12 12v8.6" /></g>}
  </svg>;
}
function StartGlyph({kind, size = kind === 'paste' ? 20 : 22}: {kind: 'link' | 'paste' | 'server'; size?: number}) {
  return <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false"><g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin={kind === 'paste' ? 'round' : undefined}>
    {kind === 'link' ? <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1" /> : kind === 'server' ? <><rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="13" width="18" height="7" rx="2" /><path d="M7 7.5h.01M7 16.5h.01" /></> : <><rect x="5" y="4.5" width="14" height="16" rx="2" /><path d="M9 4.5V3.5h6v1M8.5 10h7M8.5 14h5" /></>}
  </g></svg>;
}

/** connect26/31: controlled content only; invitation preview/session belongs to Apps. */
export function ServicesStart({header, invitation, onInvitationChange, onInvitationPaste, onJoin, onPaste, onDownload, onApps, onCopyDownload, onShareDownload, error, children, returning = false, notice}: {
  header: ReactNode; invitation: string; onInvitationChange: (value: string) => void; onJoin: () => void; onPaste: () => void;
  onInvitationPaste?: (value: string) => void;
  onDownload: () => void; onApps: () => void; onCopyDownload: () => void; onShareDownload: () => void; error?: ReactNode; children?: ReactNode; returning?: boolean; notice?: ReactNode;
}) {
  return <div className={styles.start}>{header}<ColourBackdrop />
    <main className={styles.startMain}><div className={styles.startIntro}><div className={styles.startHeading}><p>{t('services.start.ready')}</p><h1>{t(returning ? 'services.start.returning' : 'services.start.welcome')}</h1><p className={styles.startLead}>{t('services.start.body')}{!returning && <span className={styles.startDesktop}> {t('services.start.free')}</span>}</p></div>
      {notice && <div className={styles.startNotice}>{notice}</div>}
      <div className={styles.startCards}><section className={styles.startJoin} id="join">
        <div className={styles.startCardHeading}><span className={styles.startIcon}><StartGlyph kind="link" /></span><h2>{t('services.start.join')}</h2></div><p>{t('services.start.joinBody')}</p>
        <form className={styles.startJoinForm} onSubmit={event => { event.preventDefault(); onJoin(); }}>
          <TextField variant="auth" aria-label={t('services.start.invitation')} placeholder={t('services.start.invitation')} value={invitation} onChange={event => onInvitationChange(event.currentTarget.value)} autoCapitalize="off" autoComplete="off" spellCheck={false}
            onPaste={event => { if (onInvitationPaste) { event.preventDefault(); onInvitationPaste(event.clipboardData.getData('text/plain')); } }}
            trailing={<button type="button" className={styles.paste} aria-label={t('services.start.paste')} onClick={onPaste}><StartGlyph kind="paste" /></button>} />
          <Button variant="primary" type="submit">{t('services.start.joinAction')}</Button>
        </form>{error}
        <p className={styles.startQuiet}><span className={styles.startDesktop}>{t('services.start.qr')}</span><span className={styles.startPhone}>{t('services.start.qrPhone')}</span></p>
        <div className={styles.startWhere}><b>{t('services.start.where')}</b><p>{t('services.start.whereBody')}</p></div>
      </section><section className={styles.startDownloads} id="setup">
        <span className={styles.startIcon}><StartGlyph kind="server" /></span><h2>{t('services.start.setup')}</h2><p>{t('services.start.setupBody')}</p>
        <div className={styles.downloads}>{downloads.map(item => <div key={item.id}><DownloadIcon kind={item.id} /><b>{t(item.title)}</b><small>{t(item.detail)}</small><Button size="small" onClick={onDownload}>{t('action.download')}</Button></div>)}</div>
        <div className={styles.startSteps}>{steps.map((item, index) => <div key={item.title}><b><i>{index + 1}</i>{t(item.title)}</b><p>{t(item.body)}</p></div>)}</div>
      </section></div>
      <div className={styles.mobileSetup}><h2>{t('services.start.setup')}</h2><button type="button" className={styles.mobileSetupLink} onClick={() => document.getElementById('setup-details')?.scrollIntoView({block: 'start'})}><span className={styles.mobileRowIcon}><StartGlyph kind="server" size={18} /></span><span>{t('services.start.serverFree')}<small>{t('services.start.notPhone')}</small></span><Icon name="i-right" size={18} /></button></div></div>
      <section className={styles.mobileSetupDetails} id="setup-details"><h2>{t('services.start.setup')}</h2><p>{t('services.start.phoneSetupBody')}</p>
        <div className={styles.mobileSetupActions}><Button size="small" icon="i-copy" onClick={onCopyDownload}>{t('services.start.copyLink')}</Button><Button size="small" icon="i-share" onClick={onShareDownload}>{t('services.start.shareLink')}</Button></div>
        <h3>{t('services.start.whatNext')}</h3><div className={styles.mobileSteps}>{steps.map((item,index) => <div key={item.title}><i>{index + 1}</i><span>{t(item.title)}<small>{t(item.body)}</small></span></div>)}</div>
        <h3>{t('services.start.apps')}</h3><div className={styles.mobileAppLinks}><button type="button" onClick={onApps}><span className={styles.mobileRowIcon}><Icon name="i-apple" size={18} /></span><span>{t('services.start.porticoIPhone')}</span><Icon name="i-right" size={18} /></button><button type="button" onClick={onApps}><span className={styles.mobileRowIcon}><Icon name="i-tv" size={18} /></span><span>{t('services.start.porticoTV')}<small>{t('services.start.allTVs')}</small></span><Icon name="i-right" size={18} /></button></div>
      </section>
      <div className={styles.startApps} id="apps"><span><b>{t('services.start.apps')}</b><small>{t('services.start.appsBody')}</small></span>{apps.map(item => <Button key={item.title} icon={item.icon} iconSize={20} size="small" onClick={onApps}>{t(item.title)}</Button>)}</div>
      <footer className={styles.startLegal}><a href="https://getportico.tv/terms/">{t('services.signup.terms')}</a><a href="https://getportico.tv/privacy/">{t('services.start.privacy')}</a></footer>
    </main>{children}
  </div>;
}
