import {forwardIdentity} from '../detail/context';
import {useLayoutEffect, useRef, useState} from 'react';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {createRecordedGroupReader, type RecordedGroupReader} from './recorded-group';
import type {RecordingsSort} from './recordings-controller';

export type RecordedGroupLease = {
  id: string;
  context: ScreenContext;
  group: string;
  sort: RecordingsSort;
  /** False once this panel's layout retirement ran, before its reader aborts. */
  live: boolean;
  /** The group's own live flag plus the exact door route witness. */
  current: () => boolean;
  reader: RecordedGroupReader;
  playLife: ReturnType<typeof screenLifetime>;
};

/** One committed recorded-group panel. The exact context, show key and sort own
 * the reader and a separate play lifetime; layout retirement retires both before
 * abort, so replay cannot reuse a disposed reader or borrow a later context. */
export function useRecordedGroupLease(context: ScreenContext | undefined, group: string, sort: RecordingsSort, doorCurrent: () => boolean): RecordedGroupLease | undefined {
  const [published, publish] = useState<RecordedGroupLease>();
  // The door witness belongs to this exact opening: keep the one this effect ran
  // with instead of recreating the reader whenever the route hands a new closure.
  const door = useRef(doorCurrent);
  useLayoutEffect(() => {door.current = doorCurrent;});
  useLayoutEffect(() => {
    if (!context || !context.isCurrent()) return;
    let held: RecordedGroupLease | undefined;
    const doorCurrent = door.current;
    const current = () => !!held && held.live && context.isCurrent() && doorCurrent();
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const reader = createRecordedGroupReader(screen, group, sort), playLife = screenLifetime(screen);
    const lease: RecordedGroupLease = {id: crypto.randomUUID(), context, group, sort, live: true, current, reader, playLife};
    held = lease;
    publish(lease);
    void reader.load().catch(() => {});
    return () => {lease.live = false;reader.dispose();playLife.dispose();};
    // doorCurrent is captured by this exact panel opening.
  }, [context, group, sort]);
  return published?.live && published.context === context && published.group === group && published.sort === sort && published.current() ? published : undefined;
}
