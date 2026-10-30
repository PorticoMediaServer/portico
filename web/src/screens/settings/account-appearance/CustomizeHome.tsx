import {useEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {HomeService} from '@core/home-service.ts';
import {PersonalSavedService} from '@core/personal-saved.ts';
import type {SavedApi} from '@core/saved.ts';
import {randomId} from '@core/random-id.ts';
import {Button} from '../../../kit/controls';
import {HomeCustomization, type HomeCustomizationBindings} from '../../home/customization';
import {HomeCustomizeDialog, homeCopy} from '../../home/Customize';
import {useShellWidth} from '../../../shell/viewport';

const t = defaultI18n.t;

export type CustomizeHomeOptions = Readonly<{
  api: SavedApi;
  home: HomeService;
  serverId: string;
  viewerId: string;
  isCurrent: () => boolean;
}>;

/** The Home layout's own dialog services: the Settings row asks the same producer and writer
 * the end of Home does, under the captured session's lifetime. */
export function customizeHomeBinding(options: CustomizeHomeOptions): HomeCustomizationBindings {
  return {isCurrent: options.isCurrent, requestId: randomId, createSavedViews: () => {
    if (!options.isCurrent()) throw new Error('shell_authority_retired');
    return new PersonalSavedService(options.api, {serverId: options.serverId, viewerId: options.viewerId}, async () => randomId());
  }};
}

/** Appearance › Customize Home: the drawn "Customize Home…" control opens the one dialog
 * Home itself draws, over Settings. The row is a profile-scoped link; it owns no layout store. */
export function CustomizeHomeRow({api, home, serverId, viewerId, isCurrent}: CustomizeHomeOptions) {
  const compact = useShellWidth() < 600, trigger = useRef<HTMLButtonElement>(null), active = useRef<HomeCustomization | undefined>(undefined);
  const [opened, setOpened] = useState<HomeCustomization>();
  const binding = useMemo(() => customizeHomeBinding({api, home, serverId, viewerId, isCurrent}), [api, home, serverId, viewerId, isCurrent]);
  const close = () => {active.current?.dispose();active.current = undefined;setOpened(undefined);};
  useEffect(() => () => {active.current?.dispose();active.current = undefined;}, [home]);
  const open = () => {
    if (!isCurrent() || active.current) return;
    const next = new HomeCustomization(home, binding);
    active.current = next; setOpened(next); void next.load();
  };
  return <>
    <Button ref={trigger} variant="glass" size="small" onClick={open}>{t('next.settingsSpec.account.appearance.groups.0.rows.0.control.label')}</Button>
    {opened && <HomeCustomizeDialog controller={opened} createSavedViews={binding.createSavedViews} isCurrent={isCurrent} copy={homeCopy(compact)} onClose={close} returnFocusRef={trigger}/>}
  </>;
}
