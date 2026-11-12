import {forwardIdentity} from '../detail/context';
import {useLayoutEffect, useRef, useState} from 'react';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {createRecordingsController, type RecordingsController, type RecordingsView} from './recordings-controller';
import type {TunerController} from '../settings/server-livetv/recording/tuner-controller';


export type RecordingsLease = {
  id: string;
  context: ScreenContext;
  view: RecordingsView;
  /** Everyone's recordings rather than this profile's own. */
  everyone: boolean;
  attempt: number;
  /** False once this door's layout retirement ran, before its controller aborts. */
  live: boolean;
  /** The exact route witness and this lease's own live flag. */
  current: () => boolean;
  life: ReturnType<typeof screenLifetime>;
  controller: RecordingsController;
  /** Admin allocation facts exist only on the owner's Storage destination. */
  tuners?: TunerController;
};

/** One committed Recordings door. The exact context, destination and retry own
 * the controller and its session; layout retirement retires them before abort,
 * so an effect replay cannot revive a disposed controller or its private facts. */
export function useRecordingsLease(context: ScreenContext | undefined, view: RecordingsView, attempt: number, routeCurrent: () => boolean, everyone = false): RecordingsLease | undefined {
  const [published, publish] = useState<RecordingsLease>();
  // The route witness belongs to this exact opening: keep the one this effect ran
  // with instead of recreating the controller whenever the route hands a new closure.
  const route = useRef(routeCurrent);
  useLayoutEffect(() => {route.current = routeCurrent;});
  useLayoutEffect(() => {
    if (!context || !context.isCurrent()) return;
    let held: RecordingsLease | undefined;
    const routeCurrent = route.current;
    const current = () => !!held && held.live && context.isCurrent() && routeCurrent();
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const life = screenLifetime(screen);
    const controller = createRecordingsController({api: life.api, serverId: context.scope.serverId,
      signal: life.signal(), isCurrent: life.current, everyone});
    // The owner's tuner list moves to the new server with the owner's Live TV screens; until then Storage leaves it out.
    const lease: RecordingsLease = {id: crypto.randomUUID(), context, view, everyone, attempt, live: true, current, life, controller};
    held = lease;
    publish(lease);
    const load = async () => {
      await controller.load({view});
      await controller.readSummary();
      if (view === 'recorded') await controller.readGroups('recent');
    };
    void load().catch(() => {});
    return () => {lease.live = false;controller.dispose();life.dispose();};
    // routeCurrent is captured by this exact door opening.
  }, [context, view, attempt, everyone]);
  return published?.live && published.context === context && published.view === view && published.everyone === everyone && published.attempt === attempt && published.current() ? published : undefined;
}
