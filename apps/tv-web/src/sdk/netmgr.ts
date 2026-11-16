/**
 * `@amazon-devices/keplerscript-netmgr-lib` in a TV browser: whether the set is online, from the
 * browser's own online and offline events. A browser says nothing of addresses or the kind of
 * link, so those stay empty and the network module treats every change as a new path.
 */
export type NetInfoState = {type: string; isConnected: boolean | null; isInternetReachable: boolean | null; details: null};

function state(): NetInfoState {
  const online = navigator.onLine !== false;
  return {type: online ? 'unknown' : 'none', isConnected: online, isInternetReachable: null, details: null};
}

export function addEventListener(listener: (state: NetInfoState) => void): () => void {
  const changed = () => listener(state());
  window.addEventListener('online', changed);
  window.addEventListener('offline', changed);
  queueMicrotask(changed);
  return () => {
    window.removeEventListener('online', changed);
    window.removeEventListener('offline', changed);
  };
}
