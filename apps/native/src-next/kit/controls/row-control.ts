import {createContext} from 'react';

/** Inside a settings row's control slot on a touch screen: a select there draws as the row's value (settings.html phone
 *  frames: the value in accent text at the row's end, never a box) and takes at most `maxWidth` (half the row), so the
 *  row's label keeps its room. Other controls (buttons, switches) keep their own width and are never cut. */
export const RowControlContext = createContext<Readonly<{maxWidth?: number}> | undefined>(undefined);
