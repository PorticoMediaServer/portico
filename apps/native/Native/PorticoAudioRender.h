#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>

// Render helper of PorticoPlaybackController. It never claims a session, renews a
// lease, changes queue order or publishes Now Playing. Device-local output requests
// go through the sole PorticoAudioProcessing owner.
@interface PorticoAudioRender : NSObject
@property(nonatomic,copy) void (^onFact)(NSDictionary *fact);
// Version 2 plans decode on the device.
@property(nonatomic,readonly) BOOL active;
@property(nonatomic,readonly) double position;
+ (NSString *)validate:(NSDictionary *)render source:(NSString *)source binding:(NSDictionary *)binding;
/** Whether a version 2 plan (spec §18.1) is one this device decodes (mode, container and codec). */
+ (BOOL)playable:(NSDictionary *)plan;
/** Deletes the audio cache, except files a deck is reading (sign-out, a profile or server switch). */
+ (void)purgeCache;
- (void)applyCommand:(NSDictionary *)command;
- (NSDictionary *)snapshot;
/** Immediate local safety gate; never allocates or changes server authority. */
- (void)pauseOutput;
- (void)invalidate;
#if DEBUG
/** §18.8 conformance (Debug builds only): with PORTICO_AUDIO_CONFORMANCE (the fixtures folder) and
 * PORTICO_AUDIO_CONFORMANCE_OUT (a JSON path) in the environment, decodes every fixture at launch. */
+ (void)runConformanceIfRequested;
/** Exit-test harness: behave as if the audio route changed (the engine stops and reconfigures). */
- (void)debugConfigurationChange;
/** Exit-test harness (INT T6): two readers of one cache key, the first still partial. Blocks; call off the main thread. */
+ (NSDictionary *)debugSharedReaders:(NSURL *)url;
#endif
@end
