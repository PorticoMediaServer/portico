#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>
@class PorticoSubtitleOverlay;
@interface PorticoPlaybackController : NSObject
@property(nonatomic,copy) void (^onFact)(NSDictionary *fact);
/** PiP events include the immutable request binding and requestId, so stale callbacks are rejected. */
@property(nonatomic,copy) void (^onPictureInPicture)(NSDictionary *change);
@property(nonatomic,readonly) AVPlayer *player;
@property(nonatomic,readonly) NSString *lease;
/** Current source-ready video binding; nil while replanning, seeking, suspended or unready. */
@property(nonatomic,copy,readonly) NSDictionary *externalPresentationBinding;
/** Actual decoded current-item frame, independent of the presentation view's bounds.
 * Main queue only. Caller owns the returned buffer. Never seeks or replaces the output.
 * The snapshot module may recover from this exact item's asset, with the same attachment,
 * 250 ms decoded-time fence and original 1500 ms total deadline. */
- (CVPixelBufferRef)copyVideoFrameForBinding:(NSDictionary *)binding displayTime:(CMTime *)displayTime transform:(CGAffineTransform *)transform CF_RETURNS_RETAINED;
@property(nonatomic,readonly) NSString *subtitleText;
@property(nonatomic,readonly) PorticoSubtitleOverlay *subtitleOverlay;
@property(nonatomic,readonly) NSDictionary *subtitleCue;
/** Portico's caption style for the line the app draws ({scale, background}), from the subtitle
 * command in force; empty when the command carries none. */
@property(nonatomic,readonly) NSDictionary *subtitleStyle;
#if TARGET_OS_TV
/** The window the picture is shown in, whose display is asked to match the title. The player
 * view sets it; without one the app's key window is used. */
@property(nonatomic,weak) UIWindow *displayWindow;
#endif
// Canonical audio controls belong to the native listening owner. JS supplies only
// attachment identity/options while this token is active.
- (void)listeningOwner:(NSString *)owner state:(NSDictionary *)state origin:(NSString *)origin allowed:(BOOL)allowed seek:(NSDictionary *)seek render:(NSDictionary *)render;
- (void)listeningRoute:(NSString *)owner ready:(BOOL)ready revision:(NSInteger)revision;
- (void)clearListeningOwner:(NSString *)owner;
- (NSDictionary *)listeningSnapshot;
- (void)resetListeningEngine:(NSString *)owner;
- (void)invalidate;
- (NSDictionary *)acquire:(NSString *)owner;
- (NSString *)acceptCommand:(NSDictionary *)command;
- (void)testClampNext;
#if DEBUG
/** Exit-test harness: the render engine behaves as if the audio route changed. */
- (void)debugAudioConfigurationChange;
#endif
- (NSDictionary *)testStaleCommands;
- (void)displayReadyForLease:(NSString *)lease;
- (void)releaseLease:(NSString *)lease;
// System integration (PorticoSystemMedia): the app's one playback controller, whether music or an
// audiobook currently owns the system surfaces, and the position in the media's own timeline.
@property(class,nonatomic,weak) PorticoPlaybackController *shared;
@property(nonatomic,readonly) BOOL listeningActive;
- (double)currentSourcePosition;
/** OS-02: the chosen subtitles are drawn by the app, so PiP and AirPlay won't carry them. */
@property(nonatomic,readonly) BOOL subtitleOverlayActive;
@end
