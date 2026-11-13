import {createContext} from 'react';

/** True inside a settings row's control slot on a touch screen: a control there draws as the row's value (settings.html
 *  phone frames: the value in accent text at the row's end, never a box), so the row's label keeps its room. */
export const RowControlContext = createContext(false);
