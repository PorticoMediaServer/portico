import React from 'react';
import {PlayerChromeFrame, type PlayerChromeFrameProps} from '../../../kit/frames';
import {PlayerControl, PlayerPill} from '../../../kit/player';
import {defaultI18n} from '../../../../../../packages/i18n/src';

type Props = Pick<PlayerChromeFrameProps, 'picture' | 'chromeVisible' | 'scrubber' | 'times' | 'tools' | 'transport' | 'prompts' | 'card' | 'scrubbing' | 'onControlsLayout'> & {
  onMinimize: () => void;
  onQuality: () => void;
  quality: string;
  onFullWindow?: () => void;
  controlsEnabled: boolean;
};

export function PlayerPage({onMinimize, onQuality, quality, onFullWindow, controlsEnabled, ...chrome}: Props) {
  const t = defaultI18n.t;
  return <PlayerChromeFrame {...chrome} form="upright" pane testID="watch-upright-frame"
    close={<PlayerControl onPicture icon="i-down" label={t('player.minimize')} testID="watch-minimize" onPress={onMinimize}/>}
    trailing={<>
      <PlayerPill icon="i-quality" value={quality} label={t('player.opt.quality')} testID="watch-option-quality"
        onPress={onQuality} disabled={!controlsEnabled}/>
      {onFullWindow && <PlayerControl tool icon="i-fs" label={t('next.states.shortcut.fullscreen')} testID="watch-fullscreen"
        onPress={onFullWindow} disabled={!controlsEnabled}/>}
    </>}/>;
}
