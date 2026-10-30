import {forwardRef, type ComponentPropsWithoutRef} from 'react';
import {RadioGroup as Primitive} from 'radix-ui';
import {stopRadioNavigation} from './radio-navigation';

/** Unstyled exclusive choices: callers retain their exact buttons and layout. */
export const RadioGroup = forwardRef<HTMLDivElement, ComponentPropsWithoutRef<typeof Primitive.Root>>(function RadioGroup({onKeyDown, ...props}, ref) {
  return <Primitive.Root {...props} ref={ref} onKeyDown={event => {onKeyDown?.(event);stopRadioNavigation(event);}}/>;
});
export const RadioItem = Primitive.Item;
