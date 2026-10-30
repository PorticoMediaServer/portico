import React, {useSyncExternalStore} from 'react';
import {Text} from 'react-native';
import {legacyChannel, legacyProgramme} from '../../../../../packages/client-core/src/guide/legacy-source';
import {Dialog, Menu, type MenuEntry} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {ProgrammeDetails} from '../settings-live/LiveScreen';
import {NativeSeriesRuleSheet} from '../settings-live/LiveSeriesRule';
import type {NativeTVLiveActions} from './live-actions';

/** Composition over the existing More menu, programme details and DVR rule sheet.
 * Their services and every callback belong to the supplied original opening. */
export function NativeLiveActionPanels({actions, context, runtime}: {actions: NativeTVLiveActions; context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const state = useSyncExternalStore(actions.subscribe, actions.getSnapshot);
  useSyncExternalStore(context.playback.subscribe, context.playback.getSnapshot);
  const theme = useTheme(), t = runtime.text.t, sheet = state.sheet;
  if (!sheet || !sheet.lifetime.isCurrent()) return null;
  const channel = legacyChannel(sheet.offer.channel), programme = legacyProgramme(sheet.offer.programme);
  if (!channel || !programme) return null;
  const close = () => {if (sheet.lifetime.isCurrent()) actions.close(sheet);};
  if (sheet.kind === 'series') return <NativeSeriesRuleSheet key={sheet.id} controller={sheet.port.recordings} draft={{channel, programme, series: true}}
    defaults={sheet.defaults} runtime={runtime} open lifetime={sheet.lifetime} onOpenChange={value => {if (!value) close();}}
    onCreated={() => actions.seriesCreated(sheet)}/>;
  if (sheet.kind === 'details') return <Dialog key={sheet.id} open onOpenChange={value => {if (!value) close();}} restoreFocus={false}
    title={sheet.offer.programme.title} placement="side" width={640} testID="watch-live-details">
    <ProgrammeDetails programme={sheet.offer.programme} now={Date.now()} runtime={runtime}/>
  </Dialog>;
  const items: MenuEntry[] = [
    ...(sheet.offer.channel.kind === 'live' && sheet.offer.channel.recordAvailable ? [{id: 'series', label: t('guide.recordSeries'), icon: 'i-repeatrule' as const,
      disabled: state.busy, closeOnSelect: false, onSelect: () => {if (sheet.lifetime.isCurrent()) void actions.series(sheet);}}] : []),
    {id: 'favorite', label: t(sheet.offer.channel.favorite ? 'channels.removeFavorite' : 'channels.addFavorite'), icon: 'i-heart', selected: sheet.offer.channel.favorite,
      disabled: state.busy, closeOnSelect: false, onSelect: () => {if (sheet.lifetime.isCurrent()) void actions.favorite(sheet, runtime.reportError);}},
    {id: 'details', label: t('action.details'), icon: 'i-info', disabled: state.busy, closeOnSelect: false,
      onSelect: () => {if (sheet.lifetime.isCurrent()) actions.details(sheet);}},
  ];
  return <Menu key={sheet.id} open onOpenChange={value => {if (!value) close();}} restoreFocus={false} title={t('next.controls.more')}
    description={sheet.offer.programme.title} items={items} presentation="sideSheet" width={640} testID="watch-live-more-menu"
    context={state.error ? <StateView {...runtime.error(state.error, () => actions.dismissError(sheet))} primaryAction={undefined} secondaryAction={undefined} additionalActions={undefined}/> : <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{sheet.offer.channel.name}</Text>}/>;
}
