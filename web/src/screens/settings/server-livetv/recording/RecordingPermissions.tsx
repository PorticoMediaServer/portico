import {useEffect,useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button,Switch} from '../../../../kit/controls';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import type {RecordingPermissionsController,RecordingPermissionRow} from './permissions-controller';
import styles from '../../../live/owner/owner.module.css';
const t=defaultI18n.t;
export function RecordingPermissions({controller}:{embedded?:boolean;controller:RecordingPermissionsController}){
 const state=useSyncExternalStore(controller.subscribe,controller.getSnapshot,controller.getSnapshot);
 useEffect(()=>{void controller.load();},[controller]);if(!controller.isActive())return null;
 const groups=new Map<string,RecordingPermissionRow[]>();for(const row of state.owners)groups.set(row.accountId,[...(groups.get(row.accountId)??[]),row]);
 const busy=state.loading||state.saving||state.uncertain;
 return <section className={styles.section}><div className={styles.heading}><h3>{t('web.dvrSettings.whoCanRecord')}</h3><Button variant="icon" icon="i-refresh" iconOnly size="small" aria-label={t('action.refresh')} disabled={state.loading||state.saving} onClick={()=>void controller.load()}/></div>
  <p className={styles.lede}>{t('web.liveTv.recordingAccountHelp')}</p>
  {!!state.error&&<p role="alert" className={styles.error}>{errorMessage(state.error)}<Button size="small" disabled={state.loading||state.saving} onClick={()=>void controller.load()}>{t('action.tryAgain')}</Button></p>}
  {state.loading&&!state.owners.length&&<p role="status">{t('status.loading')}</p>}
  {[...groups].map(([id,rows])=>{const account=rows[0]!;return <section key={id} className={styles.sources}>
   <SettingsRow label={account.accountName} help={account.ownerAccount?t('web.liveTv.ownerMayRecord'):t('web.liveTv.accountMayRecord')}><Switch checked={account.accountAllowed} disabled={busy||account.ownerAccount} aria-label={t('web.dvrSettings.allowRecording')+': '+account.accountName} onCheckedChange={enabled=>void controller.change(account,enabled,true)}/></SettingsRow>
   {rows.map(row=><SettingsRow key={row.profileId} label={row.profileName} help={t(row.canRecord?'next.liveOwner.canRecord':'next.liveOwner.cannotRecord')}><Switch checked={row.accountAllowed&&row.profileAllowed} disabled={busy||!row.accountAllowed} aria-label={t('web.dvrSettings.allowRecording')+': '+row.profileName} onCheckedChange={enabled=>void controller.change(row,enabled)}/></SettingsRow>)}
  </section>;})}
 </section>;
}
