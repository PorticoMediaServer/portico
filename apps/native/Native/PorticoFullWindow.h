#import <TargetConditionals.h>
#import <UIKit/UIKit.h>

#if !TARGET_OS_TV

/// The set of interface orientations the app answers its delegate with: the app's own set while
/// no player request is active, or the player's landscape set while one is. The module and the
/// AppDelegate answer must agree, because `requestGeometryUpdate` is admitted only when they do.
FOUNDATION_EXPORT UIInterfaceOrientationMask PorticoFullWindowSupportedOrientations(void);

/// The React host window the app created for its scene. The player refuses any other window, so
/// a request can only ever turn the host the viewer is actually looking at.
FOUNDATION_EXPORT void PorticoFullWindowAdoptHostWindow(UIWindow *window);

#endif
