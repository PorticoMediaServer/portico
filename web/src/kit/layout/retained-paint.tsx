import {createContext, useContext} from 'react';

/** A retained original tree may paint, but its hold/preview work has been withdrawn. */
export const RetainedLayoutPaint = createContext(false);
export const useRetainedLayoutPaint = () => useContext(RetainedLayoutPaint);
