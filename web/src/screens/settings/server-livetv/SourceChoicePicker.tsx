import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, TextField} from '../../../kit/controls';
import {SettingsSelectionList} from '../../../kit/settings';
import {dialogAction} from '../../live/owner/dialog-lifetime';
import type {SourceChoicesController} from './source-choices-controller';
const t = defaultI18n.t;
export function SourceChoicePicker({controller, value, change, disabled}: {controller: SourceChoicesController; value: readonly string[]; change: (value: readonly string[]) => void; disabled: boolean}) {
  const owner = useRef(controller);owner.current = controller;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const [query, setQuery] = useState(''), [open, setOpen] = useState(value.length > 0 || controller.kind === 'groups');
  useEffect(() => {if (!open) return;const timer = setTimeout(() => {if (owner.current === controller && controller.isActive()) void controller.load(query);}, query ? 300 : 0);return () => clearTimeout(timer);}, [controller, open, query]);
  const act = (callback: () => void) => dialogAction(owner, controller, callback);
  return <section>
    <div><b>{t(controller.kind === 'groups' ? 'server.live.onlyGroups' : 'server.live.onlyChannels')}</b>
      <Button variant="text" size="small" disabled={disabled} onClick={act(() => {change([]);})}>{t('server.live.chooseAll')}</Button>
      <Button variant="text" size="small" disabled={disabled} onClick={act(() => setOpen(value => !value))}>{t('server.live.choose')}</Button></div>
    <p>{t(controller.kind === 'groups' ? 'server.live.pickGroupsHelp' : 'server.live.pickChannelsHelp')}</p>
    {open && <><TextField value={query} disabled={disabled} type="search" aria-label={t(controller.kind === 'groups' ? 'server.live.searchGroups' : 'server.live.searchChannels')} onChange={event => setQuery(event.currentTarget.value)}/>
      <SettingsSelectionList label={t(controller.kind === 'groups' ? 'server.live.onlyGroups' : 'server.live.onlyChannels')}
        items={(state.page?.items ?? []).map(item => ({id: item.id, label: item.name}))} selected={value} disabled={disabled}
        onChange={next => {if (owner.current === controller && controller.isActive()) change(next);}}/>
      {!!state.error && <p role="alert">{errorMessage(state.error)}</p>}
      {state.page?.nextCursor && <Button size="small" loading={state.loading} disabled={disabled} onClick={act(() => void controller.load(query, true))}>{t('action.showMore')}</Button>}
    </>}
  </section>;
}
