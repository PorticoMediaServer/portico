#import <AVFoundation/AVFoundation.h>
FOUNDATION_EXPORT NSNotificationName const PorticoAudioProcessingChanged;
/* Device-local DSP only. The listening owner retains queue, account and playback authority. */
@interface PorticoAudioProcessing : NSObject
@property(class,nonatomic,readonly) PorticoAudioProcessing *shared;
@property(nonatomic,copy,readonly) NSString *sampleRateMatching;
- (BOOL)configureRate:(double)rate channels:(AVAudioChannelCount)channels error:(NSError **)error;
- (BOOL)setSampleRateMatching:(NSString *)mode error:(NSError **)error;
- (NSDictionary *)snapshot;
- (BOOL)setEqualizer:(NSDictionary *)settings error:(NSError **)error;
- (BOOL)resetEqualizer:(NSError **)error;
- (void)applyEqualizer:(AVAudioUnitEQ *)equalizer;
@end
