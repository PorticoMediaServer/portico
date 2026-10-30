import {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {UploadSourceController} from './upload-source-controller';
import styles from './owner.module.css';
import {drawnActionAllowed} from './dialog-lifetime';
const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
type UploadLease = {id: string; live: boolean; current: () => boolean; retire: () => void; controller: UploadSourceController};
type UploadIdentity = Readonly<{api: LibraryContentApi; serverId: string; isCurrent: () => boolean; reportError: (error: unknown) => void; openingId: string; opening: {live: boolean}}>;
const sameIdentity = (a: UploadIdentity, b: UploadIdentity) => a.api === b.api && a.serverId === b.serverId && a.isCurrent === b.isCurrent && a.reportError === b.reportError && a.openingId === b.openingId && a.opening === b.opening;

/** The upload dialog owns its controller in a committed layout lease. Close
 * retires the lease, the in-memory input and every read before the page closes
 * it; Strict effect replay creates a fresh controller. */
export function UploadSourceDialog({api, serverId, isCurrent, reportError, openingId, opening, close, saved}: {
  api: LibraryContentApi; serverId: string; isCurrent: () => boolean; reportError: (error: unknown) => void; openingId: string; opening: {live: boolean}; close: () => void; saved: () => void;
}) {
  const identity: UploadIdentity = {api, serverId, isCurrent, reportError, openingId, opening};
  const [published, publish] = useState<Readonly<{identity: UploadIdentity; lease: UploadLease}>>();
  useLayoutEffect(() => {
    let live = true;
    const current = () => live && opening.live && isCurrent();
    const controller = new UploadSourceController(api, serverId, current, reportError);
    const lease: UploadLease = {id: openingId, live: true, current, retire: () => {
      // One retirement owns both the closure and the published lease before disposal, so no
      // retained close/saved/edit/read handler from this opening can act after it.
      live = false;lease.live = false;controller.dispose();
    }, controller};
    publish({identity: {api, serverId, isCurrent, reportError, openingId, opening}, lease});
    return () => lease.retire();
  }, [api, serverId, isCurrent, reportError, openingId, opening]);
  const admitted = published && sameIdentity(published.identity, identity) && opening.live && published.lease.live && published.lease.current() ? published.lease : undefined;
  const state = useSyncExternalStore(admitted?.controller.subscribe ?? noSubscribe, admitted?.controller.getSnapshot ?? noSnapshot, admitted?.controller.getSnapshot ?? noSnapshot);
  const closeCurrent = () => {
    if (!admitted?.current()) return;
    admitted.retire();close();
  };
  const savedCurrent = () => {
    if (!admitted?.current()) return;
    admitted.retire();saved();
  };
  if (!admitted || !state) return null;
  const {draft, preview, busy, reading, error} = state;
  const edit = (patch: Parameters<typeof admitted.controller.edit>[0]) => {if (admitted.current()) admitted.controller.edit(patch);};
  const read = (field: 'playlist' | 'guide', file: File) => {if (admitted.current()) void admitted.controller.read(field, () => file.text());};
  const submit = () => {
    if (!admitted.current()) return;
    const snapshot = admitted.controller.getSnapshot();
    // A retained submit may only commit the exact state and preview this render drew.
    if (!drawnActionAllowed(snapshot, state, preview === undefined || snapshot.preview === preview)) return;
    if (preview) void admitted.controller.save().then(ok => {if (ok && admitted.current()) savedCurrent();});
    else void admitted.controller.check();
  };
  return <Dialog open width={560} title={t('web.liveTv.upload')} description={t('web.liveTv.uploadLede')} onOpenChange={open => {if (!open) closeCurrent();}}
    footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={busy || reading}
      disabled={!draft.name.trim() || !draft.playlist} onClick={submit}>{t(preview ? 'web.liveTv.addSource' : 'web.liveTv.checkFiles')}</Button></>}>
    <div className={styles.fields}>
      <TextField label={t('profile.name')} value={draft.name} maxLength={100} disabled={busy} autoFocus onChange={event => edit({name: event.currentTarget.value})}/>
      <TextField label={t('web.liveTv.playlistFile')} type="file" accept=".m3u,.m3u8,text/plain" disabled={busy} onChange={event => {const file = event.currentTarget.files?.[0];if (file) read('playlist', file);}}/>
      <TextField label={t('web.liveTv.guideFile')} type="file" accept=".xml,text/xml,application/xml" disabled={busy} onChange={event => {const file = event.currentTarget.files?.[0];if (file) read('guide', file);}}/>
      <TextField label={t('web.dvrSettings.tuners')} description={t('web.liveTv.tunersHelp')} type="number" min={0} max={64} step={1} value={draft.tunerCount} disabled={busy}
        onChange={event => {const value = event.currentTarget.valueAsNumber;if (Number.isSafeInteger(value) && value >= 0 && value <= 64) edit({tunerCount: value});}}/>
      {preview && <div className={styles.success} role="status"><b className={styles.facts}><span>{t('server.live.channelCount', {count: preview.channels})}</span><span>{t('server.live.programCount', {count: preview.programmes})}</span></b><p>{preview.names?.slice(0, 6).join(', ')}</p></div>}
      {!!error && <p role="alert" className={styles.error}>{errorMessage(error)}</p>}
    </div>
  </Dialog>;
}
