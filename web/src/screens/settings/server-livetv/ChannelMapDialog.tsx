import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {Icon} from '../../../kit/Icon';
import {ChannelMark} from '../../../kit/artwork';
import {dialogAction} from '../../live/owner/dialog-lifetime';
import type {ChannelMapController} from './channel-map-controller';
import styles from './source-settings.module.css';
const t = defaultI18n.t;
export function ChannelMapDialog({controller, name, close}: {controller: ChannelMapController; name: string; close: () => void}) {
  const owner = useRef(controller);owner.current = controller;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), [query, setQuery] = useState('');
  useEffect(() => {void controller.load();return () => controller.dispose();}, [controller]);
  const act = (callback: () => void) => dialogAction(owner, controller, callback), changed = Object.keys(state.edits).length;
  const closeCurrent = act(() => {controller.dispose();if (owner.current === controller) close();});
  const shown = query.trim() ? state.rows.filter(row => `${row.name} ${row.number} ${row.group}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())) : state.rows;
  const disabled = state.loading || state.saving || state.uncertain;
  return <Dialog open width={760} title={t('web.liveSource.channelMapTitle', {name})} description={t('web.liveSource.channelMapLede')} onOpenChange={open => {if (!open) closeCurrent();}}
    footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.close')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!changed || state.loading}
      onClick={act(() => void controller.save())}>{changed ? t('web.liveSource.saveMapChanges', {changed}) : t('action.save')}</Button></>}>
    <div className={styles.fields}>
      {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
      {state.partial && state.partial.saved < state.partial.total && <p role="status">{t('web.liveSource.channelMapPartial', state.partial)}</p>}
      <TextField variant="search" type="search" value={query} placeholder={t('web.live.findChannel')} aria-label={t('web.live.findChannel')} onChange={event => setQuery(event.currentTarget.value)}/>
      <div className={styles.map}><table><thead><tr>{(['channel', 'fromSource', 'number', 'guideId', 'hidden'] as const).map(id => <th key={id} scope="col">{t(`server.live.map.${id}`)}</th>)}</tr></thead><tbody>
        {shown.map(row => {const value = controller.value(row);return <tr key={row.channelId} data-changed={!!state.edits[row.channelId] || undefined} data-hidden={value.hidden || undefined}>
          <th scope="row"><span className={styles.mapChannel}><span className={styles.mapMark}>{row.mark ? <ChannelMark mark={{sourceId: controller.sourceId, id: row.channelId, ...row.mark}} size={28}/> : <Icon name="i-tv" size={18}/>}</span>{row.name}</span></th><td data-label={t('server.live.map.fromSource')}>{row.sourceNumber}</td>
          <td data-label={t('server.live.map.number')} data-changed={value.number !== (row.overridden ? row.number : '') || undefined}><TextField variant="setting" value={value.number} maxLength={12} disabled={disabled} placeholder={row.number} aria-label={t('web.liveSource.numberFor', {name: row.name})} onChange={event => controller.edit(row, {number: event.currentTarget.value})}/></td>
          <td data-label={t('server.live.map.guideId')} data-changed={value.guideChannelId !== row.guideChannelId || undefined}><TextField variant="setting" value={value.guideChannelId} maxLength={200} disabled={disabled} placeholder={t('player.quality.automatic')} aria-label={t('web.liveSource.guideFor', {name: row.name})} onChange={event => controller.edit(row, {guideChannelId: event.currentTarget.value})}/></td>
          <td data-label={t('server.live.map.hidden')} data-changed={value.hidden !== row.hidden || undefined}><Switch checked={value.hidden} disabled={disabled} aria-label={t('web.liveSource.hideChannel', {name: row.name})} onCheckedChange={hidden => controller.edit(row, {hidden})}/></td>
        </tr>;})}
      </tbody></table>
      {!state.loading && !state.rows.length && <p>{t('web.liveSource.noChannels')}</p>}
      </div>
      {state.cursor && <Button size="small" loading={state.loading} disabled={state.saving || state.uncertain} onClick={act(() => void controller.load(true))}>{t('action.showMoreChannels')}</Button>}
      {state.partial && <p>{t('next.liveOwner.mapBatchHelp')}</p>}
    </div>
  </Dialog>;
}
