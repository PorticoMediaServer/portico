import {useRef, useState, type ReactNode} from 'react';
import {createI18n, deviceRegion, resolveRegion} from '@i18n';
import {Icon, type IconName} from '../Icon';
import {Button} from '../controls';
import {Menu} from '../overlays/Popover';
import {ColourBackdrop} from '../artwork';
import styles from './server-directory-page.module.css';

const t = createI18n(resolveRegion(undefined, deviceRegion())).t;

/** One row of the account's own server list, as the caller already knows it.
 * Presence text arrives formatted by its owner; nothing here reads or guesses it. */
export type DirectoryServer = Readonly<{id: string; name: string; online: boolean; presence?: string; ownedByMe: boolean}>;

/** connect 1 and 28/31c: the root server directory. Presentation only — the
 * caller owns ordering, opening, retrying, the countdown and every guard. */
export function ServicesServerDirectory({header, servers, offline, connecting = false, seconds, pendingId, error, signedInAs, checked, busy = false, onOpen, onAccount, onJoin, onSetup, onRefresh, onDifferentAccount, onLocal, onRemove}: {
  header: ReactNode;
  servers: readonly DirectoryServer[];
  offline: boolean;
  connecting?: boolean;
  seconds: number;
  pendingId?: string;
  error?: ReactNode;
  /** The signed-in account's own display name or username, from the caller. */
  signedInAs: string;
  /** When the directory was last read, already worded by the caller. */
  checked: string;
  /** A directory refresh is in flight somewhere other than a row. */
  busy?: boolean;
  onOpen: (id: string) => void;
  onAccount: () => void;
  onJoin: () => void;
  onSetup: () => void;
  onRefresh: () => void;
  onDifferentAccount: () => void;
  onLocal: () => void;
  /** The row's ⋯ menu (P2-66 D-3): Remove for its owner, Leave for anyone else. */
  onRemove?: (id: string) => void;
}) {
  const owned = servers.some(server => server.ownedByMe);
  const open = (server: DirectoryServer) => <button key={server.id} type="button" className={offline ? styles.sl : styles.sr}
    data-offline={!server.online || undefined} disabled={busy || pendingId !== undefined} aria-busy={pendingId === server.id || undefined}
    onClick={() => onOpen(server.id)}>
    <span className={styles.tile}><Icon name="i-server" size={22} /></span>
    <span className={styles.text}><b>{server.name}</b>
      {server.online ? <small className={styles.online}>{t('web.hostedAccount.serverOnline')}</small> : server.presence ? <small>{server.presence}</small> : null}</span>
    {server.ownedByMe && <span className={styles.owner}>{t('web.hostedAccount.serverOwner')}</span>}
    {offline ? <span className={styles.tryAgain}>{t('web.chooser.tryAgain')}</span> : <Icon name="i-right" size={20} />}
  </button>;
  const row = (server: DirectoryServer) => onRemove ? <div key={server.id} className={styles.rowWithMenu}>{open(server)}
    <RowMenu server={server} disabled={busy || pendingId !== undefined} onRemove={() => onRemove(server.id)} /></div> : open(server);
  const work = (icon: IconName, title: string, body: ReactNode, onClick: () => void) => <button type="button" className={styles.work} onClick={onClick}>
    <span className={styles.workIcon}><Icon name={icon} size={20} /></span>
    <span className={styles.text}><b>{title}</b>{body && <small>{body}</small>}</span>
    <Icon name="i-right" size={20} />
  </button>;

  return <div className={styles.page}>
    {header}<ColourBackdrop />
    <main className={styles.main}>
      <header className={styles.head}>
        <p className={styles.why}>{offline ? t('web.chooser.offlineWhy') : t('web.chooser.signedInAs', {name: signedInAs})}</p>
        <h1>{t(connecting ? 'web.chooser.connectingToServer' : offline ? 'web.chooser.offlineTitle' : 'web.chooser.title')}</h1>
        <p className={styles.lede}>{offline
          ? <><span className={styles.desktopOnly}>{t('web.chooser.offlineBody')}</span><span className={styles.phoneOnly}>{t('web.chooser.offlineBodyPhone')}</span></>
          : t('web.chooser.lede')}</p>
      </header>
      {error}
      {offline ? <div className={styles.cards}>
        <section className={styles.card}>
          <h2 className={styles.serversHeading}>{t('web.chooser.yourServers')}</h2>
          <div className={styles.serverRows}>{servers.map(row)}</div>
          <div className={styles.alive}>
            <svg className={styles.ring} viewBox="0 0 24 24" aria-hidden="true"><circle className={styles.ringTrack} cx="12" cy="12" r="9" /><circle className={styles.ringArc} cx="12" cy="12" r="9" /></svg>
            {t('web.chooser.checkingAgain', {seconds})}
          </div>
          {owned && !connecting && <p className={styles.hint}>{t('web.hostedAccount.offlineOwnerHint')}</p>}
        </section>
        <section className={styles.card}>
          <h2>{t('web.hostedAccount.accountActions')}</h2>
          <div className={styles.workRows}>
            {work('i-person', t('web.hostedAccount.stillWorksAccount'), t('web.hostedAccount.stillWorksAccountBody'), onAccount)}
            {work('i-link', t('web.hostedAccount.stillWorksJoin'), <span className={styles.desktopOnly}>{t('web.hostedAccount.stillWorksJoinBody')}</span>, onJoin)}
            {work('i-server', t('web.hostedAccount.stillWorksSetup'), <span className={styles.desktopOnly}>{t('web.hostedAccount.stillWorksSetupBody')}</span>, onSetup)}
          </div>
        </section>
      </div> : <>
        <div className={styles.list}>{servers.map(row)}</div>
        <div className={styles.foot}>
          <Button size="small" icon="i-refresh" loading={busy} disabled={busy || pendingId !== undefined} onClick={onRefresh}>{t('web.chooser.refresh')}</Button>
          <span className={styles.checked}>{t('web.servers.checked', {when: checked})}</span>
        </div>
        <div className={styles.quietLinks}>
          <button type="button" className={styles.quietLink} onClick={onDifferentAccount}>{t('web.chooser.differentAccount')}</button>
          <button type="button" className={styles.quietLink} onClick={onLocal}>{t('web.chooser.localServer')}</button>
        </div>
      </>}
    </main>
    <footer className={styles.legal}><a href="https://getportico.tv/terms/">{t('services.signup.terms')}</a><a href="https://getportico.tv/privacy/">{t('services.start.privacy')}</a></footer>
  </div>;
}

function RowMenu({server, disabled, onRemove}: {server: DirectoryServer; disabled: boolean; onRemove: () => void}) {
  const [shown, setShown] = useState(false), anchor = useRef<HTMLButtonElement>(null);
  return <>
    <Button ref={anchor} className={styles.rowMore} size="xs" variant="icon" icon="i-more" iconOnly aria-label={t('next.home.moreFor', {title: server.name})} disabled={disabled} onClick={() => setShown(value => !value)} />
    <Menu open={shown && !disabled} onOpenChange={setShown} anchorRef={anchor} title={server.name} returnFocusRef={anchor} items={[
      {id: 'remove', icon: 'i-trash', destructive: true, label: t(server.ownedByMe ? 'web.chooser.removeServer' : 'web.chooser.leaveServer'), onSelect: () => {setShown(false); onRemove();}},
    ]} />
  </>;
}
