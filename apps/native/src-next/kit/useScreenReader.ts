import {useEffect, useState} from 'react';
import {AccessibilityInfo} from 'react-native';

/** Keep controls available while VoiceOver or TalkBack owns interaction. */
export function useScreenReader(pending = false): boolean {
  const [enabled, setEnabled] = useState(pending);
  useEffect(() => {
    let live = true, changed = false;
    const subscription = AccessibilityInfo.addEventListener('screenReaderChanged', value => {
      changed = true;
      if (live) setEnabled(value);
    });
    void AccessibilityInfo.isScreenReaderEnabled().then(value => {if (live && !changed) setEnabled(value);}, () => {if (live) console.warn('portico_screen_reader_query_failed');});
    return () => {live = false; subscription.remove();};
  }, []);
  return enabled;
}
