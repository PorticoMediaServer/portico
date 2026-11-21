import React, {type ComponentProps, type RefObject} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {IconName} from '../../../kit/Icon';
import {PlayerChromeFrame, type PlayerChromeFrameProps} from '../../../kit/frames';
import {PlayerControl, PlayerPill, PlayerTitle} from '../../../kit/player';

/** One pill under the bar: Episodes, Subtitles, Audio or Quality, each opening its panel. */
export type SidewaysPill = Readonly<{id: string; icon: IconName; name: string; value?: string; open: boolean}>;

type Props = Pick<PlayerChromeFrameProps, 'picture' | 'chromeVisible' | 'scrubber' | 'times' | 'transport' | 'prompts' | 'card'
  | 'scrubbing' | 'onControlsLayout'> & {
  identity?: ComponentProps<typeof PlayerTitle>;
  controlsEnabled: boolean;
  onMinimize: () => void;
  onCast?: () => void;
  castRef?: RefObject<View | null>;
  onPictureInPicture?: () => void;
  onOptions: () => void;
  /** Episodes, at the start of the pill row. */
  leading?: SidewaysPill;
  /** Subtitles, Audio and Quality, at its end. */
  pills: readonly SidewaysPill[];
  onPill: (id: string) => void;
};

/** The video player on a phone, which turns sideways only here (phone-player 1): Close and the title over the picture with the
 * tools opposite, the transport in the middle, then position, bar and time left on one line, and the pills under it. */
export function SidewaysPlayerPage({identity, controlsEnabled, onMinimize, onCast, castRef, onPictureInPicture, onOptions, leading, pills,
  onPill, ...chrome}: Props) {
  const t = defaultI18n.t;
  const pill = (value: SidewaysPill) => <PlayerPill key={value.id} icon={value.icon} name={value.name} value={value.value} label={value.name}
    testID={`watch-option-${value.id}`} open={value.open} disabled={!controlsEnabled} onPress={() => onPill(value.id)}/>;
  return <PlayerChromeFrame {...chrome} form="sideways" pane testID="watch-sideways-frame"
    close={<PlayerControl onPicture icon="i-down" label={t('player.minimize')} testID="watch-minimize" onPress={onMinimize}/>}
    title={identity && <PlayerTitle {...identity}/>}
    tools={<>
      {onCast && <PlayerControl tool icon="i-cast" label={t('player.playOn')} testID="watch-cast" anchorRef={castRef}
        disabled={!controlsEnabled} onPress={onCast}/>}
      {onPictureInPicture && <PlayerControl tool icon="i-pip" label={t('player.pictureInPicture')} testID="watch-pip"
        disabled={!controlsEnabled} onPress={onPictureInPicture}/>}
      <PlayerControl tool icon="i-sliders" label={t('player.opt.menu')} testID="watch-options" disabled={!controlsEnabled} onPress={onOptions}/>
    </>}
    leading={leading && pill(leading)}
    trailing={<>{pills.map(pill)}</>}/>;
}
