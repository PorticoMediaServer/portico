import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../../kit/controls';
import {Dialog} from '../../../../kit/overlays/Dialog';
import type {SettingsLibraryChannelsSource} from './channels-source';
import styles from './channels.module.css';
const t = defaultI18n.t;

/** The answer is the server's health record for the exact selected channel. */
export function ChannelHealth({source}:Readonly<{source:SettingsLibraryChannelsSource}>) {
  const state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot), channel=state.selected, health=state.health;
  const close=()=>{if(channel)source.closeHealth(channel);};
  return <Dialog open={!!channel} onOpenChange={open=>{if(!open)close();}} title={channel?.config.name ?? ''} description={t('web.libraryChannels.healthLede')} width={480}
    footer={<><Button size="compact" variant="glass" icon="i-refresh" disabled={state.healthLoading} onClick={()=>{if(channel && source.getSnapshot().selected===channel)void source.health(channel);}}>{t('device.approval.checkAgain')}</Button><Button size="compact" variant="primary" onClick={close}>{t('action.done')}</Button></>}>
    <div className={styles.health}>
      {state.healthLoading && <p role="status">{t('next.settings.loading')}</p>}
      {state.healthError && <p role="alert" className={styles.warning}>{t('error.unavailable')}</p>}
      {health && <><p className={health.healthy ? styles.healthy:styles.warning}>{health.message || t(health.healthy ? 'web.libraryChannels.onAir':'web.libraryChannels.offAir')}</p>
        <dl className={styles.facts}>
          <div><dt>{t('web.libraryChannels.healthSchedule')}</dt><dd>{health.generatedThrough ? defaultI18n.formatDate(health.generatedThrough,{weekday:'short',month:'short',day:'numeric',hour:'numeric',minute:'2-digit'}):t('web.libraryChannels.healthNotGenerated')}</dd></div>
          <div><dt>{t('web.libraryChannels.healthPrograms')}</dt><dd>{defaultI18n.number(health.scheduledEntries)}</dd></div>
          <div><dt>{t('web.libraryChannels.healthCandidates')}</dt><dd>{defaultI18n.number(health.candidates)}</dd></div>
          {health.unresolved>0 && <div><dt>{t('web.libraryChannels.healthUnresolved')}</dt><dd>{defaultI18n.number(health.unresolved)}</dd></div>}
          <div><dt>{t('web.libraryChannels.healthNewSchedule')}</dt><dd>{t(health.pendingGeneration ? 'web.libraryChannels.healthBeingMade':'web.libraryChannels.healthUpToDate')}</dd></div>
        </dl></>}
    </div>
  </Dialog>;
}
