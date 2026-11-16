import React, {useMemo} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {View, type ViewProps} from 'react-native';

/**
 * `@react-native-masked-view/masked-view` in a TV browser: the mask element (always an SVG of
 * gradients in the shared kit) is drawn once to markup and used as a CSS alpha mask, stretched
 * over the view. The browser composites it on the graphics chip; nothing runs per frame.
 */
type Props = ViewProps & {maskElement: React.ReactElement; androidRenderingMode?: string; children?: React.ReactNode};

export default function MaskedView({maskElement, androidRenderingMode: _mode, style, children, ...view}: Props) {
  const image = useMemo(() => `url("data:image/svg+xml;charset=utf-8,${encodeURIComponent(renderToStaticMarkup(maskElement))}")`, [maskElement]);
  const mask = {WebkitMaskImage: image, maskImage: image, WebkitMaskSize: '100% 100%', maskSize: '100% 100%', WebkitMaskRepeat: 'no-repeat', maskRepeat: 'no-repeat'};
  return <View {...view} style={[style, mask as object]}>{children}</View>;
}
