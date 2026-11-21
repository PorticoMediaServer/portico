import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {ChannelMark} from '../artwork';
import {Button} from '../controls';
import {liveNextLine, liveRecordLabel, liveTimeRange} from './live-model';
import type {LiveChromePort} from './live-types';
import styles from './live-page.module.css';

const t = defaultI18n.t;
const time = (ms: number) => new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {hour: 'numeric', minute: '2-digit', timeZone: defaultI18n.region.timeZone ?? undefined}).format(ms);

/** Under an upright live picture: the channel's page (phone-player 1c and 1e, phone-app 11; the owner's 18 Nov ruling that
 * the strip names nothing and the page below names the channel and programme). The channel's mark, name, number and Live;
 * what is on; its times and what follows; Guide and Record. The native kit draws the same page. */
export function LivePage({port, reportError}: Readonly<{port: LiveChromePort; reportError: (error: unknown) => void}>) {
  const [busy, setBusy] = useState(false);
  const {channel, programme} = port, next = liveNextLine(port), recordLabel = liveRecordLabel(programme);
  const range = programme ? liveTimeRange(programme.startMs, programme.endMs, time) : undefined;
  const record = () => {
    if (busy || !port.isCurrent()) return;
    setBusy(true);
    void port.record().then(() => {}, reportError).finally(() => setBusy(false));
  };
  return <section className={styles.page} aria-label={channel.name}>
    <p className={styles.channel}>
      {channel.mark && <ChannelMark className={styles.mark} mark={{sourceId: channel.sourceId, id: channel.id, ...channel.mark}} size={44}/>}
      <b>{channel.name}</b>{channel.number && <span>{channel.number}</span>}<span className={styles.live}><i/>{t('guide.live')}</span>
    </p>
    <h1 className={styles.title}>{programme?.title ?? channel.name}</h1>
    {(range || next) && <p className={styles.when}>{range && <span>{range}</span>}{next && <span>{next}</span>}</p>}
    <div className={styles.actions}>
      <Button variant="glass" icon="i-list" onClick={() => {if (port.isCurrent()) port.openFullGuide();}}>{t('player.live.guide')}</Button>
      {channel.recordAvailable && programme && recordLabel && <Button className={styles.record} variant="glass" icon="i-rec" disabled={busy} onClick={record}>{recordLabel}</Button>}
    </div>
  </section>;
}
