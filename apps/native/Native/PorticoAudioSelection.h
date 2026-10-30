#import <AVFoundation/AVFoundation.h>
@interface PorticoAudioSelection : NSObject
@property(nonatomic,copy) void (^onFact)(NSDictionary *fact);
- (void)apply:(NSDictionary *)command item:(AVPlayerItem *)item;
- (void)invalidate;
@end

/** Actual supported options for one admitted local AVPlayer item. Catalogue tokens expire
 * with the item; a command is applied only after AVPlayer's selection is read back. */
@interface PorticoLocalAudioSelection : NSObject
@property(nonatomic,copy) void (^onFact)(NSDictionary *fact);
- (void)apply:(NSDictionary *)command item:(AVPlayerItem *)item;
- (void)invalidate;
@end
