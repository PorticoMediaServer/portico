import {useEffect, useRef, useState} from 'react';
import {useLocation} from '@tanstack/react-router';
import {router} from '../../router';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {ProtectedLibraryScreen} from './screen-context';
import {retainMusicMixes} from './music-mixes-owner';
import {bindMusicMixes, type MusicMixesBinding} from './music-mixes-binding';

export function useMusicMixes(captured: ProtectedLibraryScreen | undefined, enabled: boolean) {
  const context = captured?.context, href = useLocation({select: location => location.href});
  const runtime = useRef(captured?.binding.playback?.runtime);runtime.current = captured?.binding.playback?.runtime;
  const [value, setValue] = useState<Readonly<{context: ShellAssemblyContext; binding: MusicMixesBinding}>>();
  useEffect(() => {
    if (!context || !enabled) return;
    const lease = retainMusicMixes(context), life = new AbortController();
    const binding = bindMusicMixes(lease.source, {signal: life.signal, isCurrent: () => context.isCurrent() && router.state.location.href === href}, () => runtime.current);
    setValue({context, binding});
    return () => {life.abort();binding.dispose();lease.release();};
  }, [context, enabled, href]);
  return enabled && value?.context === context ? value?.binding : undefined;
}
