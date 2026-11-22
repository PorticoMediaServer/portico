import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {SectionCard} from '../../../kit/frames';
import {BottomSheet, MenuRow} from '../../../kit/overlays';
import {StorageActions, StorageNote, StorageSheetBody} from '../../../kit/settings/StorageParts';
import {LiveSourcesEmpty} from '../../../kit/live/LiveSourceParts';
import {HealthFacts, LibraryChannelCard, MenuGroup} from '../../../kit/live/ChannelParts';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {NativeChannelEditor} from '../../../kit/live/channels/ChannelEditor';
import {NativeBlockPresets} from '../../../kit/live/channels/BlockPresets';
import {NativeChannelLogo} from '../../../kit/live/channels/ChannelLogo';
import {useChannelsGroup, useLibraryChannels} from '../../shared/live/useLibraryChannels';

const t = defaultI18n.t;

/** Live TV & Channels › Library Channels: New channel and Refresh, the owner's channels, block presets, and each
 * channel's menu. The editor, presets and logo sheets are still the old ones (their own PRs). */
export function LibraryChannelsTab({ports, runtime, initialChannelId}: {ports: Pick<NativeSettingsLivePorts, 'context' | 'requestId'>; runtime: NativeShellRuntime; initialChannelId?: string}) {
  const group = useChannelsGroup(ports, runtime);
  return group ? <LibraryChannelsList group={group} runtime={runtime} initialChannelId={initialChannelId}/> : null;
}

export function LibraryChannelsList({group, runtime, initialChannelId}: {group: NativeChannelsGroup; runtime: NativeShellRuntime; initialChannelId?: string}) {
  const view = useLibraryChannels({group, runtime, initialChannelId}), {state, blocked, held, health, removal} = view;
  return <>
    <SectionCard plain label={t('settings.server.libraryChannels')}>
      <StorageActions onPage>
        <Button testID="channels-new" disabled={blocked || !state.choices} onPress={view.newChannel}>{t('builder.newTitle')}</Button>
        <Button size="compact" variant="glass" disabled={blocked} onPress={view.refresh}>{t('web.selection.refresh')}</Button>
        <NativeBlockPresets group={group}/>
      </StorageActions>
    </SectionCard>
    <SectionCard plain>
      {state.loading && <StorageNote onPage>{t('next.settings.loading')}</StorageNote>}
      {view.error !== undefined && <><StorageNote alert onPage>{view.error}</StorageNote>
        <StorageActions onPage><Button size="compact" onPress={view.retry}>{t('action.tryAgain')}</Button></StorageActions></>}
      {!state.loading && !state.channels.length && <LiveSourcesEmpty testID="channels-empty" title={t('builder.empty.title')} body={t('builder.empty.body')}/>}
      {view.rows.map(row => <LibraryChannelCard key={row.key} testID={`channel-${row.key}`} name={row.name} mark={row.mark} logo={row.logo} titles={row.titles}
        status={row.status} tone={row.tone} through={row.through} summary={row.summary} disabled={blocked} onPress={row.press}/>)}
      {view.more && <StorageActions onPage><Button size="compact" disabled={blocked} onPress={view.more}>{t('action.showMore')}</Button></StorageActions>}
    </SectionCard>
    <BottomSheet open={view.templatesOpen} onOpenChange={value => {if (!value) view.closeTemplates();}} title={t('builder.newTitle')}>
      <MenuGroup>
        {view.templates.map(template => <MenuRow key={template.key} variant="grouped" icon="i-tv" label={template.name} description={!template.available ? t('server.channels.notEnough') : undefined}
          disabled={blocked || !template.available} onPress={() => view.open(template)}/>)}
        <MenuRow variant="grouped" icon="i-plus" label={t('web.libraryChannels.startFromScratch')} disabled={blocked} onPress={() => view.open()}/>
      </MenuGroup>
    </BottomSheet>
    <BottomSheet open={!!held} onOpenChange={value => {if (!value) view.closeMenu();}} title={held?.name}>
      {held && <MenuGroup>
        <MenuRow variant="grouped" icon="i-edit" label={t('web.selection.edit')} disabled={blocked} onPress={view.edit}/>
        <MenuRow variant="grouped" icon="i-pulse" label={t('web.libraryChannels.checkHealth')} disabled={blocked} onPress={view.checkHealth}/>
        <NativeChannelLogo group={group} channel={held}/>
        <MenuRow variant="grouped" icon={held.on ? 'i-pause' : 'i-play'} label={t(held.on ? 'web.libraryChannels.turnOff' : 'web.libraryChannels.turnOn')} disabled={blocked} onPress={view.toggle}/>
        <MenuRow variant="grouped" icon="i-refresh" label={t('web.libraryChannels.regenSchedule')} disabled={blocked} onPress={view.regenerate}/>
        <MenuRow variant="grouped" icon="i-up" label={t('web.profile.moveUp')} disabled={blocked || view.first} onPress={view.up}/>
        <MenuRow variant="grouped" icon="i-down" label={t('web.profile.moveDown')} disabled={blocked || view.last} onPress={view.down}/>
        <MenuRow variant="grouped" icon="i-trash" label={t('web.libraryChannels.delete')} destructive disabled={blocked} onPress={view.askRemove}/>
      </MenuGroup>}
    </BottomSheet>
    <BottomSheet open={!!removal} onOpenChange={value => {if (!value && !state.busy) view.closeRemove();}} title={removal?.name} description={t('web.libraryChannels.deleteBody')}
      footer={<><Button variant="glass" disabled={state.busy} onPress={view.closeRemove}>{t('action.cancel')}</Button>
        <Button variant="danger" disabled={state.busy} onPress={view.confirmRemove}>{t('action.delete')}</Button></>}>
      {null}
    </BottomSheet>
    <BottomSheet open={!!health} onOpenChange={value => {if (!value) health?.close();}} title={health?.name} description={t('web.libraryChannels.healthLede')}>
      {health && <StorageSheetBody>
        <HealthFacts headline={health.headline} facts={health.facts}/>
        <Button onPress={health.again}>{t('server.remote.checkAgain')}</Button>
      </StorageSheetBody>}
    </BottomSheet>
    {state.editor && <NativeChannelEditor key={state.editor.key} group={group} runtime={runtime} editor={state.editor}/>}
  </>;
}
