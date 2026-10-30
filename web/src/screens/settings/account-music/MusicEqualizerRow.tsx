import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../../../kit/overlays';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {BrowserSettingsEqualizer, createBrowserEqualizerPort, type EqualizerPort} from '../../../kit/media';
import type {SettingsPlatform} from '../../../platform/settings-platform';

const t = defaultI18n.t;
const noSubscribe = () => () => {};

/** The EQ row exists only where a real app/device store and its controlled component are bound.
 * The browser always has the renderer's device-local store; a native host supplies both. */
export function musicEqualizerAvailable(platform: SettingsPlatform | undefined): boolean {
  return platform?.environment.platform === 'web' || platform?.Equalizer !== undefined || platform?.equalizerPort !== undefined;
}

/** Account Music › Equalizer: the drawn link shows the store's real On/Off state and opens
 * the same device EQ the player edits. The browser uses one port for both the row and the
 * page, so a change is visible without a second read; a native host supplies its own. */
export function MusicEqualizerRow({platform, isCurrent}: Readonly<{
  platform?: SettingsPlatform; isCurrent: () => boolean;
}>) {
  const trigger = useRef<HTMLButtonElement>(null);
  const [opened, setOpened] = useState(false);
  const [error, setError] = useState<unknown>();
  const browser = platform?.environment.platform === 'web' && platform?.Equalizer === undefined;
  const [browserPort] = useState<EqualizerPort | undefined>(() => browser ? createBrowserEqualizerPort(value => {if (isCurrent()) setError(value);}) : undefined);
  const port = platform?.equalizerPort ?? browserPort;
  useEffect(() => {
    if (!port) return;
    const stop = port.start();
    return stop;
  }, [port]);
  const snapshot = useSyncExternalStore(port?.subscribe ?? noSubscribe, () => port?.getSnapshot(), () => port?.getSnapshot());
  const Component = platform?.Equalizer;
  const failed = (value: unknown) => {if (isCurrent()) setError(value);};
  const close = (open: boolean) => {
    if (open) { setError(undefined); setOpened(true); return; }
    setOpened(false); setError(undefined);
  };
  return <>
    <Button ref={trigger} variant="text" size="small" style={{color: 'var(--color-accent-strong)'}} onClick={() => close(true)}>
      <span style={{display: 'inline-flex', alignItems: 'center', gap: 4}}>{t(snapshot?.enabled === true ? 'next.settings.on' : 'next.settings.off')}<Icon name="i-right" size={16} /></span>
    </Button>
    <Dialog open={opened} onOpenChange={close} title={t('next.media.eq.title')} width={1000} returnFocusRef={trigger}>
      {error !== undefined && <p role="alert">{t('next.states.pageFailedBody')}</p>}
      {Component ? <Component layout="settings" onError={failed} />
        : browser ? <BrowserSettingsEqualizer layout="settings" onError={failed} {...(port ? {port} : {})} /> : null}
    </Dialog>
  </>;
}
