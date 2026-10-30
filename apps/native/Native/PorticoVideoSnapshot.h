#import <Foundation/Foundation.h>
@class PorticoPlaybackController;
/** Main-queue lookup of the actual retained player view. Bounds/visibility are not authority. */
FOUNDATION_EXPORT PorticoPlaybackController *PorticoSnapshotController(NSNumber *reactTag, NSString *lease);
