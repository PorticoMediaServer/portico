import {useState} from 'react';

/** The front-door stages publish a pending transaction as `visible: false` under
 * one fixed stage revision, while every control in the stage detaches for the
 * duration. The shared entry helper is one receipt per revision and stays that
 * way for rail, overlay and content callers, so a stage counts its own completed
 * pending state and asks for its declared entry child again. The adjustment runs
 * during render, as React supports for state this component owns, so the first
 * ready render already carries the new revision and the entry layout effect that
 * follows it runs once. A stage that mounts ready asks once, exactly as before;
 * a stage that mounts pending enters once when it ends. */
export function useTVBusyEntryRevision(busy: boolean): number {
  const [previous, setPrevious] = useState(busy), [revision, setRevision] = useState(0);
  if (previous !== busy) {
    setPrevious(busy);
    if (previous && !busy) setRevision(revision + 1);
  }
  return revision;
}
