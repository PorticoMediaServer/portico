#import <UIKit/UIKit.h>

/// The first frame, seamlessly (DESIGN "The first frame, seamlessly"): covers the React host
/// window with the launch storyboard's own view, so the system's launch screen, this cover and
/// React's Boot are the same pixels. `PorticoRuntime.bootDrawn` (React's first frame) removes it
/// without animation. Called once, after React Native has started in the window.
FOUNDATION_EXPORT void PorticoBootCoverInstall(UIWindow *window);
