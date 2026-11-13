import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {Icon} from '../../../kit/Icon';
import {dialogAction} from '../../live/owner/dialog-lifetime';
import {validChannelNumber, type ChannelMapController} from './channel-map-controller';
import styles from './source-settings.module.css';
const t = defaultI18n.t;
export function ChannelMapDialog({controller, name, close}: {controller: ChannelMapController; name: string; close: () => void}) {
  const owner = useRef(controller);owner.current = controller;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), [query, setQuery] = useState('');
  useEffect(() => () => controller.dispose(), [controller]);
  const act = (callback: () => void) => dialogAction(owner, controller, callback), changed = Object.keys(state.edits).length;
  const closeCurrent = act(() => {controller.dispose();if (owner.current === controller) close();});
  useEffect(() => {const timer=setTimeout(() => {if(controller.isActive())void controller.load(false,query);},query?300:0);return()=>clearTimeout(timer);},[controller,query]);
  const shown=state.rows, invalid=Object.values(state.edits).some(value=>!validChannelNumber(value.number));
  const disabled = state.loading || state.saving;
  return <Dialog footerDismisses open width={760} title={t('web.liveSource.channelMapTitle', {name})} description={t('web.liveSource.channelMapLede')} onOpenChange={open => {if (!open) closeCurrent();}}
    footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.close')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!changed || state.loading || invalid}
      onClick={act(() => void controller.save())}>{changed ? t('web.liveSource.saveMapChanges', {changed}) : t('action.save')}</Button></>}>
    <div className={styles.fields}>
      {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error,'server-console',{operation:'action'})}</p>}
      {state.partial && state.partial.saved < state.partial.total && <p role="status">{t('web.liveSource.channelMapPartial', state.partial)}</p>}
      <TextField variant="search" type="search" value={query} disabled={!!changed || state.loading || state.saving} maxLength={256} placeholder={t('web.live.findChannel')} aria-label={t('web.live.findChannel')} onChange={event => setQuery(event.currentTarget.value)}/>
      <div className={styles.map}><table><thead><tr>{(['channel', 'fromSource', 'number', 'guideSource', 'guideId', 'hidden'] as const).map(id => <th key={id} scope="col">{t(`server.live.map.${id}`)}</th>)}</tr></thead><tbody>
        {shown.map(row => {const value = controller.value(row);return <tr key={row.id} data-changed={!!state.edits[row.id] || undefined} data-hidden={value.hidden || undefined}>
          <th scope="row"><span className={styles.mapChannel}><span className={styles.mapMark}><Icon name="i-tv" size={18}/></span>{row.name}</span></th><td data-label={t('server.live.map.fromSource')}>{row.sourceNumber}</td>
          <td data-label={t('server.live.map.number')} data-changed={value.number !== row.number || undefined}><TextField variant="setting" value={value.number} maxLength={12} disabled={disabled} error={!validChannelNumber(value.number)?t('web.liveSource.channelNumberInvalid'):undefined} aria-label={t('web.liveSource.numberFor', {name: row.name})} onChange={event => controller.edit(row, {number: event.currentTarget.value})}/></td>
          <td data-label={t('server.live.map.guideSource')}><Select aria-label={t('server.live.map.guideSource')} value={value.guideSource} disabled={disabled} options={[{value:'',label:t('web.dvr.none')},...state.guides.map(source=>({value:source.id,label:source.name}))]} onValueChange={guideSource=>controller.edit(row,{guideSource})}/></td>
          <td data-label={t('server.live.map.guideId')} data-changed={value.guideKey !== (row.guideKey??'') || undefined}><TextField variant="setting" value={value.guideKey} maxLength={200} disabled={disabled} aria-label={t('web.liveSource.guideFor', {name: row.name})} onChange={event => controller.edit(row, {guideKey: event.currentTarget.value})}/></td>
          <td data-label={t('server.live.map.hidden')} data-changed={value.hidden !== row.hidden || undefined}><Switch checked={value.hidden} disabled={disabled} aria-label={t('web.liveSource.hideChannel', {name: row.name})} onCheckedChange={hidden => controller.edit(row, {hidden})}/></td>
        </tr>;})}
      </tbody></table>
      {!state.loading && !state.rows.length && <p>{t('web.liveSource.noChannels')}</p>}
      </div>
      {state.cursor && <Button size="small" loading={state.loading} disabled={state.saving} onClick={act(() => void controller.load(true))}>{t('action.showMoreChannels')}</Button>}
    </div>
  </Dialog>;
}
