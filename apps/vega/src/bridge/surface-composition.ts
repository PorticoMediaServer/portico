import {createContext} from 'react';

/** Absent for legacy hosts; the current shared Surface supplies its outer composition intent. */
export const VegaSurfaceComposition = createContext<boolean | undefined>(undefined);
