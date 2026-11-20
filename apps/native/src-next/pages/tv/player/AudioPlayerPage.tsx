import React from 'react';
import {AudioPlayerFrame, type AudioPlayerFrameProps} from '../../../kit/frames';
import {defaultI18n} from '../../../../../../packages/i18n/src';

type Props = Omit<AudioPlayerFrameProps, 'close'> & {onClose: () => void};

export function TVAudioPlayerPage({onClose, ...player}: Props) {
  return <AudioPlayerFrame {...player} close={{icon: 'i-down', label: defaultI18n.t('player.minimize'), onPress: onClose}}/>;
}
