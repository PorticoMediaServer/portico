#import <AVFoundation/AVFoundation.h>
#if DEBUG
/* Opt-in debug evidence at the actual engine output boundary, after mixing/EQ/resampling.
 * Never proves a physical DAC/HDMI output. A dropped block fails the capture explicitly. */
@interface PorticoAudioCapture : NSObject
- (void)start:(AVAudioMixerNode *)mixer;
- (void)stop;
- (NSDictionary *)snapshot;
@end
#endif
