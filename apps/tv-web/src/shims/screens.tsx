import {View} from 'react-native';

// `react-native-screens` is used only by the touch shell's stacks (`shell/TouchStacks.tsx`),
// which a television never mounts. The names exist so the shared shell bundles.
export const ScreenStack = View;
export const ScreenStackItem = View;
export type StackAnimationTypes = string;
