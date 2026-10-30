import {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import styles from './owner.module.css';
const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
type DiscoveryLease = {id: string; live: boolean; current: () => boolean; retire: () => void; controller: DiscoveryController};
type DiscoveryIdentity = Readonly<{api: LibraryContentApi; serverId: string; isCurrent: () => boolean; reportError: (error: unknown) => void; openingId: string; opening: {live: boolean}}>;
const sameIdentity = (a: DiscoveryIdentity, b: DiscoveryIdentity) => a.api === b.api && a.serverId === b.serverId && a.isCurrent === b.isCurrent && a.reportError === b.reportError && a.openingId === b.openingId && a.opening === b.opening;
import {DiscoveryController} from './discovery-controller';
import {drawnActionAllowed} from './dialog-lifetime';
/** Discovery does not save or confirm a local address. Use this opens the
 * ordinary source check, including its server-required LAN confirmation. */
export function DiscoverTuners({api, serverId, isCurrent, reportError, openingId, opening, close, onUseTuner}: {
  api: LibraryContentApi; serverId: string; isCurrent: () => boolean; reportError: (error: unknown) => void; openingId: string; opening: {live: boolean}; close: () => void; onUseTuner: (tuner: string) => void;
}) {
  const identity: DiscoveryIdentity = {api, serverId, isCurrent, reportError, openingId, opening};
  const [published, publish] = useState<Readonly<{identity: DiscoveryIdentity; lease: DiscoveryLease}>>();
  useLayoutEffect(() => {
    let live = true;
    const current = () => live && opening.live && isCurrent();
    const controller = new DiscoveryController(api, serverId, current, reportError);
    const lease: DiscoveryLease = {id: openingId, live: true, current, retire: () => {
      // One retirement owns both the closure and the published lease before disposal, so no
      // retained close/search/use handler from this opening can act after it.
      live = false;lease.live = false;controller.dispose();
    }, controller};
    publish({identity: {api, serverId, isCurrent, reportError, openingId, opening}, lease});
    void controller.search();
    return () => lease.retire();
  }, [api, serverId, isCurrent, reportError, openingId, opening]);
  const admitted = published && sameIdentity(published.identity, identity) && opening.live && published.lease.live && published.lease.current() ? published.lease : undefined;
  const state = useSyncExternalStore(admitted?.controller.subscribe ?? noSubscribe, admitted?.controller.getSnapshot ?? noSnapshot, admitted?.controller.getSnapshot ?? noSnapshot);
  const closeCurrent = () => {
    if (!admitted?.current()) return;
    admitted.retire();close();
  };
  if (!admitted || !state) return null;
  const {busy, result, error} = state;
  const searchAgain = () => {if (admitted.current()) void admitted.controller.search();};
  /** The press must still belong to the drawn result, with this exact resident tuner still
   * unconfigured and no search in flight; retire this opening before handing it to the page. */
  const chooseTuner = (tuner: NonNullable<typeof result>['devices'][number]) => {
    if (!admitted.current()) return;
    const snapshot = admitted.controller.getSnapshot();
    if (!drawnActionAllowed(snapshot, state, !snapshot.busy && !!snapshot.result && snapshot.result === result && snapshot.result.devices.includes(tuner) && !tuner.configured)) return;
    admitted.retire();onUseTuner(tuner.baseUrl);
  };
  return <Dialog open width={520} title={t('web.liveSource.discoverTitle')} description={t('web.liveSource.discoverLede')} onOpenChange={open => {if (!open) closeCurrent();}}
    footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.close')}</Button><Button size="small" icon="i-refresh" loading={busy} onClick={searchAgain}>{t('web.liveSource.searchAgain')}</Button></>}>
    <div className={styles.fields}>
      {busy && <p role="status">{t('web.liveSource.searching')}</p>}
      {!!error && <p role="alert" className={styles.error}>{errorMessage(error)}</p>}
      {result && !result.discoveryEnabled && <p>{t('web.liveSource.searchOff')}</p>}
      {result?.discoveryEnabled && !result.devices.length && <p>{t('web.liveSource.noTuners')}</p>}
      {result?.devices.map(tuner => <SettingsRow key={tuner.deviceId || tuner.address} label={tuner.friendlyName || tuner.model || t('web.liveSource.tunerFallback')}
        help={<span className={styles.facts}>{[tuner.model, tuner.address, tuner.tunerCount ? t('server.live.tunerCount', {count: tuner.tunerCount}) : ''].filter(Boolean).map((fact, index) => <span key={index}>{fact}</span>)}</span>}>
        {tuner.configured ? <span className={styles.badge}>{t('web.liveSource.alreadyAdded')}</span> : <Button size="small" onClick={() => chooseTuner(tuner)}>{t('web.liveSource.useThis')}</Button>}
      </SettingsRow>)}
    </div>
  </Dialog>;
}
