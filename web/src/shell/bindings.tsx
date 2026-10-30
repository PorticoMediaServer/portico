import {createContext, useContext, type ReactNode} from 'react';
import type {HttpLocalApi} from '@core/index.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {HomeBindings} from '../screens/home/Home';
import type {ShellProps} from './Shell';
import type {ShellPlaybackBinding} from './Playback';

/** One verified, current session. The runtime owner fences/disposes it on authority changes. */
export type ShellBindings = Readonly<{
  api: HttpLocalApi;
  events: EventsClient;
  scopeKey: string;
  shell: Omit<ShellProps, 'children'>;
  home: HomeBindings;
  playback?: ShellPlaybackBinding;
}>;
const Context = createContext<ShellBindings | undefined>(undefined);
export function ShellBindingsProvider({value, children}: {value: ShellBindings | undefined; children: ReactNode}) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useShellBindings(): ShellBindings | undefined { return useContext(Context); }
