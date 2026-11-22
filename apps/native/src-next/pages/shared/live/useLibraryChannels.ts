import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {LibraryChannel, LibraryChannelTemplate} from '../../../../../../packages/contracts/src/next/api';
import {channelMonograms} from '../../../../../../packages/client-core/src/presentation/kit-model/channels';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {createNativeChannels, type NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';

const t = defaultI18n.t;
export type ChannelTone = 'quiet' | 'healthy' | 'building' | 'warning' | 'danger';

/** The page and its sheets share one admitted owner lifetime, made again when the server context changes. */
export function useChannelsGroup(ports: Pick<NativeSettingsLivePorts, 'context'>, runtime: NativeShellRuntime) {
  const context = ports.context, [group, setGroup] = useState<NativeChannelsGroup>();
  useLayoutEffect(() => {const next = createNativeChannels(context, runtime.reportError); setGroup(next); return next.dispose;}, [context, runtime.reportError]);
  return group?.context === context ? group : undefined;
}

/** Live TV & Channels › Library Channels: the owner's channels with what each holds and how it's doing, a new channel
 * from a template or from scratch, and each channel's menu (edit, health, logo, on/off, regenerate, move, delete). Every
 * opening is held by a token so a replaced menu or sheet can neither act nor dismiss its successor. Moved as it was. */
export function useLibraryChannels({group, runtime, initialChannelId}: {group: NativeChannelsGroup; runtime: Pick<NativeShellRuntime, 'artwork'>; initialChannelId?: string}) {
  const {source} = group, state = useSyncExternalStore(source.subscribe, source.getSnapshot);
  const [menu, setMenu] = useState<{row: LibraryChannel; token: object}>(), [templates, setTemplates] = useState<object>(), [remove, setRemove] = useState<{row: LibraryChannel; token: object}>();
  const openings = useMemo(() => ({active: group.current(), menu: undefined as typeof menu, templates: undefined as object | undefined, remove: undefined as typeof remove}), [group]);
  useLayoutEffect(() => {openings.active = group.current(); return () => {openings.active = false; openings.menu = undefined; openings.templates = undefined; openings.remove = undefined;};}, [group, openings]);
  if (openings.menu && !state.channels.includes(openings.menu.row)) openings.menu = undefined;
  if (openings.remove && !state.channels.includes(openings.remove.row)) openings.remove = undefined;
  const closeMenu = () => {if (openings.active && openings.menu === menu) {openings.menu = undefined; setMenu(undefined);}};
  const closeTemplates = () => {if (openings.active && openings.templates === templates) {openings.templates = undefined; setTemplates(undefined);}};
  const closeRemove = () => {if (openings.active && openings.remove === remove) {openings.remove = undefined; setRemove(undefined);}};
  useEffect(() => {void source.load().then(() => {if (initialChannelId && group.current()) void source.openId(initialChannelId);});}, [group, source, initialChannelId]);
  const blocked = !!(state.loading || state.busy || state.uncertain || state.editor || state.presetEditor || state.pendingCreate || state.pendingPreset);
  const held = menu && openings.menu === menu && state.channels.includes(menu.row) && group.current() ? menu : undefined;
  const removal = remove && openings.remove === remove && state.channels.includes(remove.row) && group.current() ? remove : undefined;
  const marks = channelMonograms({kind: 'complete-authorized', channels: state.channels.map(row => ({sourceId: row.id, id: row.id, name: row.name}))});
  const open = (template?: LibraryChannelTemplate) => {if (openings.active && templates && openings.templates === templates && !blocked && group.current() && (!template || state.choices?.items.includes(template) && template.available)) {source.blank(template); closeTemplates();}};
  const action = (run: (row: LibraryChannel) => void) => {if (openings.active && held && openings.menu === held && !blocked && group.current() && source.getSnapshot().channels.includes(held.row)) {closeMenu(); run(held.row);}};
  const rows = state.channels.map(row => {
    const word: MessageId = !row.on ? 'web.libraryChannels.offState' : row.state === 'onAir' ? 'web.libraryChannels.onAir' : row.state === 'building' ? 'web.libraryChannels.healthBeingMade' : row.state === 'needsSchedule' ? 'web.ownerChannels.needsSchedule' : 'web.ownerChannels.needsAttention';
    const tone: ChannelTone = !row.on ? 'quiet' : row.state === 'onAir' ? 'healthy' : row.state === 'building' ? 'building' : row.state === 'needsSchedule' ? 'warning' : 'danger';
    return {key: row.id, name: row.name, mark: marks.get(row.id)!.get(row.id)!, logo: row.logo ? runtime.artwork(row.logo) : undefined, titles: t('server.channels.titles', {count: row.titles}),
      status: t(word), tone, through: row.scheduledThrough ? t('server.channels.through', {when: defaultI18n.dateTime(row.scheduledThrough)}) : undefined, summary: row.summary || undefined,
      press: () => {if (!blocked && group.current() && source.getSnapshot().channels.includes(row)) {const next = {row, token: {}}; openings.menu = next; setMenu(next);}}};
  });
  const health = state.health && {name: state.health.name, headline: state.health.health?.message ?? state.health.problem ?? t(state.health.health?.onAir ? 'web.libraryChannels.onAir' : 'web.libraryChannels.offAir'),
    facts: state.health.health ? [`${t('web.libraryChannels.healthPrograms')}: ${defaultI18n.number(state.health.health.programmes)}`, `${t('web.libraryChannels.healthCandidates')}: ${defaultI18n.number(state.health.health.titles)}`,
      `${t('web.libraryChannels.healthUnresolved')}: ${defaultI18n.number(state.health.health.unplaceable)}`, state.health.health.reaches ? defaultI18n.dateTime(state.health.health.reaches) : t('web.libraryChannels.healthNotGenerated')] : [],
    again: () => {if (state.health) void source.health(state.health);}, close: () => source.closeHealth()};
  const heldIndex = held ? state.channels.indexOf(held.row) : -1;
  return {state, blocked, rows, health,
    newChannel: () => {if (!blocked && group.current()) {const token = {}; openings.templates = token; setTemplates(token);}},
    refresh: () => {if (group.current()) void source.load();}, more: state.cursor ? () => {void source.load(true);} : undefined,
    error: state.error ? t(state.pendingCreate ? 'error.unconfirmedChange' : 'error.unavailable') : undefined, retry: () => {if (state.pendingCreate) source.resume(); else void source.load();},
    templatesOpen: !!templates && openings.templates === templates && group.current(), closeTemplates, templates: state.choices?.items ?? [], open,
    held: held?.row, closeMenu, first: heldIndex === 0, last: heldIndex === state.channels.length - 1 && !state.cursor,
    edit: () => action(row => {void source.open(row);}), checkHealth: () => action(row => {void source.health(row);}), toggle: () => action(row => {void source.action(row, 'toggle');}),
    regenerate: () => action(row => {void source.action(row, 'regenerate');}), up: () => action(row => {void source.action(row, 'up');}), down: () => action(row => {void source.action(row, 'down');}),
    askRemove: () => action(row => {const next = {row, token: {}}; openings.remove = next; setRemove(next);}),
    removal: removal?.row, closeRemove, confirmRemove: () => {if (openings.active && removal && openings.remove === removal && group.current()) void source.action(removal.row, 'remove').then(() => {if (openings.active && openings.remove === removal && group.current()) closeRemove();});}};
}
