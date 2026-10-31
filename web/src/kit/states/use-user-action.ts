import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {createUserAction, describeActionError, type UserAction, type UserActionOptions, type UserActionSnapshot} from '@core/user-action.ts';

/** One user action per mount (packages/client-core/src/user-action.ts). run and describe are read
 *  through a ref, so the latest props apply; timings and binding are fixed at mount. Unmount
 *  cancels the attempt in flight, so its late result never lands on a screen that has gone. */
export function useUserAction<T, A extends readonly unknown[] = []>(options: UserActionOptions<T, A>): readonly [UserActionSnapshot<T>, UserAction<T, A>] {
  const latest = useRef(options);
  latest.current = options;
  const [action] = useState(() => createUserAction<T, A>({
    ...options,
    run: (context, ...args) => latest.current.run(context, ...args),
    describe: error => (latest.current.describe ?? describeActionError)(error),
  }));
  useEffect(() => () => action.cancel(), [action]);
  return [useSyncExternalStore(action.subscribe, action.getSnapshot, action.getSnapshot), action] as const;
}
