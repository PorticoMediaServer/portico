import {useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {presentError} from '@core/presentation/errors.ts';
import type {OwnerRemoteSettings} from '@core/server-admin/remote-settings.ts';
import {Button, TextField, Switch} from '../../../kit/controls';
import {Banner} from '../../../kit/overlays';
import {Dialog} from '../../../kit/overlays/Dialog';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import styles from './server-remote.module.css';

const t = defaultI18n.t;
const K = 'next.settingsSpec.server.remote.groups.2.rows.0.children.0.children.0';
const B = 'next.settingsSpec.server.remote.groups.2.rows.1.children.0';
const C = 'next.settingsSpec.server.remote.groups.1.rows.0';
const when = (value: string) => defaultI18n.dateTime(value);

/** The checked outside answer is distinct from router mapping and public-address discovery. */
export function OwnerRemotePage({source, groups, advanced, onConfirmed}: {source: OwnerRemoteSettings; groups: ReactNode; advanced: ReactNode; onConfirmed: () => void}) {
 const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), status = state.status;
 const outside = status?.outside, answers = outside?.addresses ?? [];
 const tone = answers.length ? answers.every(item => item.reachable) ? 'ok' : 'warn' : 'quiet';
 const certificate = status?.managedCertificate;
 return <div className={styles.page}>
  <section className={styles.reach} data-tone={tone} aria-label={t('next.remoteStatus.reachability')}>
   <div><strong className={styles.reachTitle} role="status">{t(state.checking ? 'settings.server.remote.checking' : !outside?.checkedAt ? 'next.remoteOwner.notChecked' : tone === 'ok' ? 'next.remoteOwner.reachable' : answers.length ? 'next.remoteOwner.unreachable' : 'next.remoteOwner.unknown')}</strong>
    {outside?.checkedAt && <p className={styles.note}>{t('web.servers.checked', {when: when(outside.checkedAt)})}</p>}
    {answers.map(item => <p key={`${item.address}:${item.port}`} className={styles.note}>{item.address}:{item.port} · {t(item.reachable ? 'next.remoteOwner.reachable' : 'next.remoteOwner.unreachable')}</p>)}
    {outside?.problem && <p className={styles.note}>{outside.problem}</p>}
   {status?.remoteSecurity.message && <p className={styles.note}>{status.remoteSecurity.message}</p>}
   {status?.remoteSecurity.problem && <p className={styles.note}>{status.remoteSecurity.problem}</p>}
   {certificate && <p className={styles.note}>{t('next.remoteStatus.secure')}: {t(certificate.ready ? 'next.remoteOwner.serving' : 'next.remoteOwner.waiting')}{certificate.name ? ` · ${certificate.name}` : ''}{certificate.expiresAt ? ` · ${t('next.remoteStatus.expires', {when: when(certificate.expiresAt)})}` : ''}</p>}
   {certificate?.problem && <p className={styles.note}>{certificate.problem}</p>}
   {certificate?.nextTryAt && <p className={styles.note}>{t('next.remoteOwner.nextTry', {when: when(certificate.nextTryAt)})}</p>}
   {status && [status.router, status.routerIPv6].filter(router => router?.state !== 'off').map((router, index) => <p key={index} className={styles.note}>{t('next.remoteOwner.router')}: {t(router.state === 'open' ? 'next.remoteOwner.mapped' : 'next.remoteOwner.notMapped')}{router.protocol ? ` · ${router.protocol}` : ''}{router.externalAddress ? ` · ${router.externalAddress}:${router.externalPort}` : ''}{router.problem ? ` · ${router.problem}` : ''}</p>)}
  </div>
   <Button size="small" variant="glass" loading={state.checking} disabled={!status || state.busy || !!state.confirmation} onClick={() => {void source.check();}}>{t('next.settingsSpec.server.general.groups.1.rows.2.control.label')}</Button>
  </section>
  {typeof state.interfaces?.document.warning === 'string' && state.interfaces.document.warning && <Banner>{state.interfaces.document.warning}</Banner>}
  {typeof state.interfaces?.document.ipv6Note === 'string' && state.interfaces.document.ipv6Note && <Banner>{state.interfaces.document.ipv6Note}</Banner>}
  {status?.warnings.map(warning => <Banner key={warning.code}>{warning.message}</Banner>)}
  {groups}{advanced}
  {state.error !== undefined && !state.confirmation && <Banner>{presentError(state.error, 'server-console', {operation: 'action'}).body}</Banner>}
  {state.confirmation && <Dialog open footerDismisses title={t('next.remoteOwner.confirm')} onOpenChange={open => {if (!open && !state.busy) source.cancelConfirmation();}}
   footer={<><Button disabled={state.busy} onClick={source.cancelConfirmation}>{t('action.cancel')}</Button><Button variant="primary" loading={state.busy} onClick={() => {void source.confirm().then(saved => {if (saved) onConfirmed();});}}>{t('next.remoteOwner.saveAnyway')}</Button></>}>
   <p>{state.confirmation.message}</p>
  </Dialog>}
 </div>;
}

/** All three certificate fields save together: one file without its key is invalid. The block draws its own rows
 * (desktop-settings remote f2): the switch at the end of its row, the address and files as its dependent rows with their
 * fields at the end, then the certificate's status (with Save while there are changes) and the identity code. */
export function OwnAddressControl({source}: {source: OwnerRemoteSettings}) {
 const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
 const saved = state.interfaces?.document.ownAddress as {enabled: boolean; domain: string; certificateFile: string; keyFile: string} | undefined;
 const [draft, setDraft] = useState(saved);
 if (!saved) return null;
 const value = draft ?? saved, locked = state.busy || !!state.confirmation, changed = JSON.stringify(value) !== JSON.stringify(saved);
 const field = (index: 0 | 1 | 2, key: 'domain' | 'certificateFile' | 'keyFile') => <SettingsRow dependent label={t(`${C}.children.${index}.label`)} help={t(`${C}.children.${index}.help`)}>
  <TextField aria-label={t(`${C}.children.${index}.label`)} placeholder={t(`${C}.children.${index}.control.placeholder`)} value={value[key]} disabled={locked} onChange={event => setDraft({...value, [key]: event.currentTarget.value})}/></SettingsRow>;
 const certificate = state.status?.certificate;
 return <form className={styles.ownAddress} onSubmit={event => {event.preventDefault();void source.saveOwnAddress(value);}}>
  <SettingsRow label={t(`${C}.label`)} help={t(`${C}.help`)}><Switch aria-label={t(`${C}.label`)} checked={value.enabled} disabled={locked} onCheckedChange={enabled => setDraft({...value, enabled})}/></SettingsRow>
  {value.enabled && <>{field(0, 'domain')}{field(1, 'certificateFile')}{field(2, 'keyFile')}</>}
  {(certificate || changed) && <SettingsRow dependent label={t(`${C}.children.3.label`)} help={certificate && [...(certificate.names ?? []), ...(certificate.expiresAt ? [t('next.remoteStatus.expires', {when: when(certificate.expiresAt)})] : [])].join(' · ') || undefined}>
   {certificate && <span className={styles.securityLine}>{certificate.problem ?? t(certificate.state === 'ok' ? 'next.remoteOwner.serving' : 'next.remoteStatus.none')}</span>}
   {changed && <Button type="submit" size="small" variant="primary" loading={state.busy} disabled={!!state.confirmation}>{t('next.remoteStatus.saveOwnAddress')}</Button>}</SettingsRow>}
  {state.status && <SettingsRow dependent label={t(`${C}.children.4.label`)} help={t(`${C}.children.4.help`)}><code className={styles.identityCode}>{state.status.identityCode}</code></SettingsRow>}
 </form>;
}

export function OwnerRefusedAddresses({source, onCertificate, onStandard}: {source: OwnerRemoteSettings; onCertificate: () => void; onStandard: () => void}) {
 const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
 const [standard, setStandard] = useState(false), names = state.status?.refused ?? [];
 return <>
  {!names.length ? <p className={styles.refusedEmpty}>{t(`${K}.control.empty`)}</p> : <ul className={styles.refused}>{names.map(item => <li key={item.address} className={styles.refusedItem}>
   <span className={styles.refusedText}><strong>{item.address}</strong><small>{t(item.reason === 'http' ? 'next.remoteOwner.refusedHttp' : 'next.remoteOwner.refusedAddress')}</small></span>
   {item.fixes.includes('allow') && <Button size="small" variant="glass" disabled={state.busy || !!state.confirmation} onClick={() => {void source.allow(item.address);}}>{t(`${K}.control.itemAction.label`)}</Button>}
   {item.fixes.includes('certificate') && <Button size="small" variant="glass" disabled={state.busy} onClick={onCertificate}>{t(`${K}.control.certificateAction.label`)}</Button>}
   {item.fixes.includes('standard') && <Button size="small" variant="glass" disabled={state.busy || !!state.confirmation} onClick={() => setStandard(true)}>{t(`${K}.control.standardAction.label`)}</Button>}
  </li>)}</ul>}
  {standard && <Dialog open footerDismisses title={t(`${K}.control.standardAction.title`)} onOpenChange={setStandard} footer={<><Button onClick={() => setStandard(false)}>{t('action.cancel')}</Button><Button variant="primary" onClick={() => {setStandard(false);onStandard();}}>{t(`${K}.control.standardAction.label`)}</Button></>}><p>{t(`${K}.control.standardAction.confirm`)}</p></Dialog>}
 </>;
}

export function OwnerBlockedNetworks({source}: {source: OwnerRemoteSettings}) {
 const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
 return !state.blocks.length ? <p className={styles.refusedEmpty}>{t(`${B}.control.empty`)}</p> : <ul className={styles.refused}>{state.blocks.map(block => <li key={block.id} className={styles.refusedItem}>
  <span className={styles.refusedText}><strong>{block.network}</strong><small>{t('next.remoteOwner.blockedUntil', {when: when(block.blockedUntil)})}</small></span>
  <Button size="small" variant="glass" disabled={state.busy || !!state.confirmation} onClick={() => {void source.unblock(block);}}>{t(`${B}.control.itemAction.label`)}</Button>
 </li>)}</ul>;
}
