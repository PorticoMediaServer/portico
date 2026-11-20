import React, {type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide';
import {Button} from '../controls';
import {FocusRegion} from '../focus';
import {Banner, Dialog} from '../overlays';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {GuideChannelArt} from './NativeGuideRow';
import {ProgrammeDetails} from './ProgrammeDetails';
import {tvGroupEdges} from '../../screens/settings-live/tv-settings-focus';

type Action = Readonly<{
  id: string; label: string; icon?: React.ComponentProps<typeof Button>['icon']; onPress: () => void;
  disabled?: boolean; loading?: boolean; selected?: boolean; primary?: boolean; opener?: RefObject<View | null>;
}>;

export type LaterProgramme = Readonly<{id: string; clock?: string; episode?: string; title: string; range?: string; actions: readonly Action[]}>;

export function ProgrammePanel({channel, programme, now, runtime, favorite, busy, paneLeft, placement, width, region, groups,
  later, reason, actions, recordError, preferenceError, onClose, opener, returnFocus}: {
  channel: GuideChannelRow; programme?: GuideProgram; now: number; runtime: NativeShellRuntime;
  favorite: () => void; busy: boolean; paneLeft?: number; placement?: 'side'; width?: number;
  region: string; groups: readonly string[]; later: readonly LaterProgramme[]; reason?: string;
  actions: readonly Readonly<{id: string; buttons: readonly Action[]}>[]; recordError?: ReactNode; preferenceError?: ReactNode;
  onClose: () => void; opener?: RefObject<View | null>; returnFocus: RefObject<View | null>;
}) {
  const theme = useTheme(), t = runtime.text.t, logo = runtime.artwork(channel.logoUrl);
  const title = programme?.title ?? channel.name, onNow = programme && programme.start <= now && programme.end > now;
  const largeLogo = theme.isTV ? theme.targets.touch : theme.space.xl;
  const header = <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, marginBottom: theme.space.sm}}>
    {(logo || channel.mark && channel.sourceId) && <GuideChannelArt channel={channel} source={logo} size={largeLogo}/>}
    <View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs, alignItems: 'center'}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{channel.name}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{channel.number}</Text>
      {paneLeft !== undefined && !!channel.group && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{channel.group}</Text>}
    </View>
    {(theme.isTV ? programme?.flags?.live : onNow) && <Text style={[theme.type.rowLabel, {color: theme.colors.record}]}>{t(theme.isTV ? 'guide.live' : 'guide.onNow')}</Text>}
    {!theme.isTV && <ProgrammeButton compact action={{id: 'live-channel-favorite', label: t(channel.favorite ? 'channels.removeFavorite' : 'channels.addFavorite'),
      icon: channel.favorite ? 'i-star-f' : 'i-star', selected: channel.favorite, disabled: busy, onPress: favorite}}/>}
  </View>;
  const group = (id: string, body: ReactNode, horizontal = false) => {
    const style = {flexDirection: horizontal ? 'row' as const : 'column' as const, gap: theme.space.xs};
    return theme.isTV ? <FocusRegion key={id} id={`${region}.${id}`} orientation={horizontal ? 'horizontal' : 'vertical'}
      edges={tvGroupEdges(groups.map(group => `${region}.${group}`), `${region}.${id}`)} stops={{left: true, right: true}} style={style}>{body}</FocusRegion>
      : <View key={id} style={style}>{body}</View>;
  };
  const laterRows = later.length > 0 && <View style={{gap: theme.space.xs}} testID="live-later">
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{t('guide.laterOn', {channel: channel.name})}</Text>
    {later.map(airing => group(`later:${airing.id}`, <>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, minWidth: theme.targets.touch}]}>{airing.clock}</Text>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{[airing.episode, airing.title].filter(Boolean).join('  ')}</Text>
        {airing.range && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{airing.range}</Text>}
      </View>
      {airing.actions.map(action => <ProgrammeButton key={action.id} action={action} compact/>)}
    </>, true))}
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('guide.reminderSet')}</Text>
  </View>;
  const body = <View style={{gap: theme.space.sm}}>
    {programme && <ProgrammeDetails programme={programme} now={now} runtime={runtime}/>}
    {theme.isTV && laterRows}
    {reason && <Banner testID="live-sheet-reason" placement="inline" variant="quiet" tone="warning" icon="i-alert" message={reason}/>}
    {actions.map(({id, buttons}) => group(id, buttons.map(action => <ProgrammeButton key={action.id} action={action}/>), !theme.isTV && buttons.length > 1))}
    {!theme.isTV && laterRows}
    {recordError && group('record-error', recordError, true)}
    {preferenceError && group('preference-error', preferenceError, true)}
  </View>;
  if (paneLeft !== undefined) return <View testID="live-programme-pane" style={{position: 'absolute', left: paneLeft, right: 0, top: 0, bottom: 0,
    backgroundColor: theme.colors.ink, borderLeftWidth: theme.space.hairline, borderLeftColor: theme.colors.line}}>
    <ScrollView contentContainerStyle={{padding: theme.space.md, gap: theme.space.sm}}>
      <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>
        <Text accessibilityRole="header" style={[theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}</Text>
        <ProgrammeButton compact action={{id: 'live-programme-close', label: t('action.close'), icon: 'i-close', onPress: onClose}}/>
      </View>
      {header}{body}
    </ScrollView>
  </View>;
  return <Dialog open onOpenChange={open => {if (!open) onClose();}} title={title} headerContext={header} placement={placement} width={width}
    initialFocusRef={opener} returnFocusRef={returnFocus} testID="live-programme">
    {theme.isTV ? <FocusRegion id={region} entryRegion={`${region}.${groups.find(id => !id.startsWith('later:'))}`}
      stops={{up: true, down: true, left: true, right: true}}>{body}</FocusRegion> : body}
  </Dialog>;
}

function ProgrammeButton({action, compact}: {action: Action; compact?: boolean}) {
  const theme = useTheme();
  return <Button ref={action.opener} testID={action.id} icon={action.icon} size={compact ? 'small' : undefined} iconOnly={compact}
    variant={compact ? 'text' : action.primary ? 'primary' : 'glass'} align={theme.isTV && !compact ? 'stretch' : undefined}
    accessibilityLabel={action.label} accessibilityState={{selected: action.selected}} disabled={action.disabled} loading={action.loading}
    focusableWhenDisabled={!compact} onPress={action.onPress}>{compact ? undefined : action.label}</Button>;
}
