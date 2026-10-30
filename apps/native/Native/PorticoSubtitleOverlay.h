#import <UIKit/UIKit.h>
#import <AVFoundation/AVFoundation.h>
@interface PorticoSubtitleOverlay : NSObject
@property(nonatomic,copy) void (^onImage)(UIImage *image);
@property(nonatomic,copy) void (^onStatus)(NSString *error);
/** Controller custody check; local resources must also belong to the current download. */
@property(nonatomic,copy) BOOL (^allowsLocalFile)(NSURL *url);
@property(nonatomic,readonly) BOOL active;
- (void)apply:(NSDictionary *)command source:(NSString *)source;
- (void)renderAtSourceTime:(double)seconds size:(CGSize)pixels;
- (void)invalidate;
@end
