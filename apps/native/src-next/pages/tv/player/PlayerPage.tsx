import React, {type ComponentProps} from 'react';
import {PlayerChromeFrame, type PlayerChromeFrameProps} from '../../../kit/frames';
import {PlayerTitle} from '../../../kit/player';

type Props = Omit<PlayerChromeFrameProps, 'close' | 'title' | 'form' | 'pane'> & {
  identity?: ComponentProps<typeof PlayerTitle>;
};

export function TVPlayerPage({identity, ...player}: Props) {
  return <PlayerChromeFrame {...player} form="tv" pane close={null}
    title={identity && <PlayerTitle {...identity}/>}/>;
}
