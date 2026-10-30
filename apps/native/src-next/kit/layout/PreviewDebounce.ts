import {useEffect, useState} from 'react';
import {createPreviewDebounce} from './model';

function usePreviewValue<T>(entry: T | undefined, repeating: boolean, delay: number, initial: T | undefined): T | undefined {
  const [shown, setShown] = useState(initial);
  useEffect(() => {
    const controller = createPreviewDebounce<T>(setShown, delay);
    if (entry !== undefined) controller.request(entry, repeating);
    else setShown(undefined);
    return () => controller.dispose();
  }, [delay, entry, repeating]);
  return entry === undefined ? undefined : shown;
}

export function usePreviewDebounce<T>(entry: T | undefined, repeating = false, delay = 250): T | undefined {
  return usePreviewValue(entry, repeating, delay, entry);
}

/** Focus preview starts unsettled, including its first supplied entry. */
export function useFocusPreviewDebounce<T>(entry: T | undefined, repeating: boolean, delay: number): T | undefined {
  return usePreviewValue(entry, repeating, delay, undefined);
}

export {createPreviewDebounce} from './model';
