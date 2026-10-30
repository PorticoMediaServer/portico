import React, {createContext, useCallback, useContext, useLayoutEffect, useRef, useState, type ReactNode} from 'react';

const Clearance = createContext(0);
type Report = (owner: object, topFromBottom: number) => () => void;
const Reporter = createContext<Report | undefined>(undefined);

/** Occupied paint geometry only, under the shell's existing authority key. */
export function DockClearanceProvider({children}: {children: ReactNode}) {
  const [paint, setPaint] = useState<{owner?: object; top: number}>({top: 0});
  const report = useCallback<Report>((owner, top) => {
    const entry = {owner, top};
    setPaint(entry);
    return () => setPaint(current => current === entry ? {top: 0} : current);
  }, []);
  return <Reporter.Provider value={report}><Clearance.Provider value={paint.top}>{children}</Clearance.Provider></Reporter.Provider>;
}

/** Report the retained drawn bar, including its ordinary exit, not playback state. */
export function useDockClearanceReport(topFromBottom: number) {
  const report = useContext(Reporter), owner = useRef({});
  useLayoutEffect(() => report?.(owner.current, topFromBottom), [report, topFromBottom]);
}
export function useDockClearance(): number {return useContext(Clearance);}
