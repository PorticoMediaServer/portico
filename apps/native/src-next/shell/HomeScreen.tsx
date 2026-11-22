import React from 'react';
import {useTheme} from '../kit/theme';
import {authorityKey, type ShellAuthority} from './model';
import {PlayOnButton} from './PlayOnButton';
import type {NativeShellRuntime} from './runtime';
import {MusicMixesRow} from '../screens/destinations/music/MusicMixesRow';
import {HomePage} from '../pages/touch/home/HomePage';
import {TVHomePage} from '../pages/tv/home/HomePage';

type HomeScreenProps = {
  runtime: NativeShellRuntime; authority: ShellAuthority;
  /** The shell hides Play on while Home is selecting or its chrome is hidden. */
  playOnHidden?: boolean; active?: boolean; heroObscured?: boolean; bottomInset?: number; onScrolledChange?: (scrolled: boolean) => void;
} & Pick<React.ComponentProps<typeof TVHomePage>, 'railExpanded' | 'holdRailDim' | 'releaseRailDim'>;

/** Home on every form: the TV page, or the touch page (phones either way up, folds and tablets). */
export function HomeScreen(props: HomeScreenProps) {
  const theme = useTheme(), {runtime, authority, active} = props;
  if (theme.isTV) return <TVHomePage {...props}/>;
  const mixes = runtime.mixes ? <MusicMixesRow focusScope={`home:${authorityKey(authority)}.mixes`} port={runtime.mixes}
    artwork={runtime.artwork} reportError={runtime.reportError} title={runtime.text.t('music.mixesForYou')}
    onOpen={runtime.musicDiscover} active={active ?? true}/> : undefined;
  const toolbar = theme.form !== 'phone' && !props.playOnHidden ? <PlayOnButton playOn={runtime.playOn} variant="round"/> : undefined;
  return <HomePage runtime={runtime} authority={authority} active={props.active} heroObscured={props.heroObscured}
    bottomInset={props.bottomInset} onScrolledChange={props.onScrolledChange} mixes={mixes} toolbar={toolbar}/>;
}
