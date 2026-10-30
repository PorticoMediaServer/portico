import type {NativeObservedHost} from './native-observer.ts';
import type {NativeObservedRect} from './native-observations.ts';

type RootGeometry = Readonly<{windowRect: NativeObservedRect; source: string}>;
type HostGeometry = Readonly<{
  box?: NativeObservedRect;
  windowRect?: NativeObservedRect;
  boxSource?: string;
  textRects?: readonly NativeObservedRect[];
  windowTextRects?: readonly NativeObservedRect[];
  textRectsSource?: string;
  unavailable: Readonly<Partial<Record<'box' | 'textRects', string>>>;
}>;

function copyRect(value: NativeObservedRect | undefined): NativeObservedRect | undefined {
  if (!value || ![value.x, value.y, value.width, value.height].every(Number.isFinite)
      || value.width < 0 || value.height < 0) return undefined;
  return Object.freeze({x: value.x, y: value.y, width: value.width, height: value.height});
}

/** One admitted native batch supplies every rectangle. This adds no attachment authority.
 * Fabric/diagnostic rectangles, display density and reference frames are not inputs. */
export function createNativeWindowGeometry(rootHost: object, observations: ReadonlyMap<object, NativeObservedHost>,
  isCurrent: () => boolean): Readonly<{root: () => RootGeometry | undefined; host: (host: object) => HostGeometry}> {
  const current = () => {try {return isCurrent();} catch {return false;}};
  const batch = new Map<object, Readonly<Pick<NativeObservedHost, 'windowRect' | 'windowRectSource' | 'textRects' | 'textRectsSource'>>>();
  if (current()) {
    for (const [host, observed] of observations) {
      const windowRect = copyRect(observed.windowRect);
      const lines = observed.textRects?.map(copyRect);
      batch.set(host, Object.freeze({windowRect, windowRectSource: observed.windowRectSource,
        textRects: lines?.every((line): line is NativeObservedRect => line !== undefined) ? Object.freeze(lines) : undefined,
        textRectsSource: observed.textRectsSource}));
    }
  }
  const root = (): RootGeometry | undefined => {
    if (!current()) return undefined;
    const observed = batch.get(rootHost);
    return observed?.windowRect && observed.windowRectSource
      ? Object.freeze({windowRect: observed.windowRect, source: observed.windowRectSource}) : undefined;
  };
  const host = (key: object): HostGeometry => {
    const origin = root();
    if (!origin) return Object.freeze({unavailable: Object.freeze({box: 'Current scoped native-window root unavailable',
      textRects: 'Current scoped native-window root unavailable'})});
    const observed = batch.get(key);
    const relative = (rect: NativeObservedRect) => copyRect({x: rect.x - origin.windowRect.x,
      y: rect.y - origin.windowRect.y, width: rect.width, height: rect.height});
    const box = observed?.windowRect && observed.windowRectSource ? relative(observed.windowRect) : undefined;
    const lines = observed?.textRects && observed.textRectsSource ? observed.textRects.map(relative) : undefined;
    const textRects = lines?.every((line): line is NativeObservedRect => line !== undefined) ? Object.freeze(lines) : undefined;
    return Object.freeze({box, windowRect: box ? observed?.windowRect : undefined,
      boxSource: box ? observed?.windowRectSource : undefined,
      textRects, windowTextRects: textRects ? observed?.textRects : undefined,
      textRectsSource: textRects ? observed?.textRectsSource : undefined,
      unavailable: Object.freeze({...(box ? {} : {box: 'Native host window bounds unavailable'}),
        ...(textRects ? {} : {textRects: 'Native window text runs unavailable'})})});
  };
  return Object.freeze({root, host});
}
