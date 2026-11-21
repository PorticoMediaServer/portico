import {frameSpec} from '@portico/design';
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

export function ProgrammePanel({channel, programme, now, runtime, favorite, busy, paneLeft, region, groups,
  later, reason, actions, recordError, preferenceError, onClose, opener, returnFocus}: {
  channel: GuideChannelRow; programme?: GuideProgram; now: number; runtime: NativeShellRuntime;
  favorite: () => void; busy: boolean; paneLeft?: number;
  region: string; groups: readonly string[]; later: readonly LaterProgramme[]; reason?: string;
  actions: readonly Readonly<{id: string; buttons: readonly Action[]}>[]; recordError?: ReactNode; preferenceError?: ReactNode;
  onClose: () => void; opener?: RefObject<View | null>; returnFocus: RefObject<View | null>;
}) {
  const theme = useTheme(), t = runtime.text.t, logo = runtime.artwork(channel.logoUrl);
  const panel = frameSpec.live.programmePanel;
  const width = theme.isTV ? panel.tv : theme.width >= panel.tabletMin ? panel.tablet : undefined;
  const placement = width === undefined ? undefined : 'side';
  const title = programme?.title ?? channel.name, onNow = programme && programme.start <= now && programme.end > now;
  const largeLogo = theme.isTV ? theme.targets.touch : theme.space.xl;
  const header = <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, marginBottom: theme.space.sm}}>
    {/* The mark fills the box it is given; the header gives it its own square at the row's start (live mock-programme). */}
    {(logo || channel.mark && channel.sourceId) && <View style={{width: largeLogo, height: largeLogo}}><GuideChannelArt channel={channel} source={logo} size={largeLogo}/></View>}
    <View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs, alignItems: 'center'}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{channel.name}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{channel.number}</Text>
      {paneLeft !== undefined && !!channel.group && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{channel.group}</Text>}
    </View>
    {/* phone-guide 2 `.live-tag`: a 7 dot and the words in the record colour at the badge size; TV keeps its row label. */}
    {theme.isTV ? programme?.flags?.live && <Text style={[theme.type.rowLabel, {color: theme.colors.record}]}>{t('guide.live')}</Text>
      : onNow && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
        <View style={{width: theme.space.tight + 1, height: theme.space.tight + 1, borderRadius: theme.space.tight, backgroundColor: theme.colors.record}}/>
        <Text style={[theme.type.badge, {color: theme.colors.record}]}>{t('guide.onNow')}</Text></View>}
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
    {/* phone-guide 2 `.nxl h5`: the small quiet label over the airings (TV keeps its row label). */}
    <Text style={theme.isTV ? [theme.type.rowLabel, {color: theme.colors.text}] : theme.type.sectionLabel}>{t('guide.laterOn', {channel: channel.name})}</Text>
    {later.map(airing => group(`later:${airing.id}`, <>
      {/* The time column of a list of airings (frameSpec.guide.timeColumn), so every title lines up under the last. */}
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, width: frameSpec.guide.timeColumn[theme.form]}]}>{airing.clock}</Text>
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
    {/* A lone action spans the sheet; side by side they share it equally (mock-programme: Watch live, then Record and Record series). */}
    {actions.map(({id, buttons}) => group(id, buttons.map(action => <ProgrammeButton key={action.id} action={action} share={!theme.isTV && buttons.length > 1}/>), !theme.isTV && buttons.length > 1))}
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

function ProgrammeButton({action, compact, share}: {action: Action; compact?: boolean; share?: boolean}) {
  const theme = useTheme();
  return <Button ref={action.opener} testID={action.id} icon={action.icon} size={compact ? 'small' : undefined} iconOnly={compact}
    variant={compact ? 'text' : action.primary ? 'primary' : 'glass'} align={compact ? undefined : 'stretch'} style={share ? {flex: 1} : undefined}
    // phone-guide 2 `.mi`: a small glyph button is quiet until it is on (a reminder set), then the accent.
    iconColor={compact && !theme.isTV ? action.selected ? theme.colors.accentStrong : theme.colors.textTertiary : undefined}
    accessibilityLabel={action.label} accessibilityState={{selected: action.selected}} disabled={action.disabled} loading={action.loading}
    focusableWhenDisabled={!compact} onPress={action.onPress}>{compact ? undefined : action.label}</Button>;
}
