#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>
// Subtitle-only adapter. The existing controller supplies AVPlayer source time.
@interface PorticoSubtitleSelection : NSObject
/** App caption style, with explicit opt-in to the system caption appearance. */
+ (void)applyCaptionAppearanceToLabel:(UILabel * _Nonnull)label baseSize:(CGFloat)base style:(NSDictionary * _Nullable)style italic:(BOOL)italic NS_SWIFT_NAME(applyCaptionAppearance(to:baseSize:style:italic:));
@property(nonatomic,copy) void (^onText)(NSDictionary *cue);
@property(nonatomic,copy) void (^onStatus)(NSString *error);
/** Whether a `file:` address is one this app may read (a file inside its own Downloads folder).
 * A downloaded title's subtitles are WebVTT files there; without this block none is read. */
@property(nonatomic,copy) BOOL (^allowsLocalFile)(NSURL *url);
- (void)apply:(NSDictionary *)command item:(AVPlayerItem *)item source:(NSString *)source;
- (void)tick:(CMTime)time;
- (void)invalidate;
/** True while a chosen track is drawn by the app rather than by AVPlayer (so it can't appear in
 * Picture in Picture or on an AirPlay receiver). */
@property(nonatomic,readonly) BOOL overlayActive;
/** Test seams (N06; apps/native/test/apple-subtitle-timeline-tests.m). `clock` is the time the
 * 40-second grant and the 25-second recheck run on (default: system uptime); `configureSession`
 * adjusts the request session's configuration before it is made; with `manualTimer` set no timer
 * runs and the test calls `timerTick` (the once-a-second check) itself. */
@property(nonatomic,copy) NSTimeInterval (^clock)(void);
@property(nonatomic,copy) void (^configureSession)(NSURLSessionConfiguration *configuration);
@property(nonatomic) BOOL manualTimer;
/** Whether the HTTP task is still delivering its response or completion on the main queue. */
@property(nonatomic,readonly) BOOL requestPending;
- (void)timerTick;
@end
