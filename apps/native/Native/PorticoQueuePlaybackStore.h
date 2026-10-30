#import <Foundation/Foundation.h>
NS_ASSUME_NONNULL_BEGIN
/** The existing device queue record, now allocated atomically by native for BOTH
 * RN queue starts and native OS controls. No second playback lane is created. */
@interface PorticoQueuePlaybackStore : NSObject
+ (nullable id)read:(NSString *)key error:(NSError **)error;
+ (BOOL)write:(NSString *)key value:(id)value error:(NSError **)error;
+ (nullable id)transaction:(NSString *)key operation:(NSString *)operation payload:(NSDictionary *)payload error:(NSError **)error;
+ (nullable id)userMutationTransaction:(NSString *)operation payload:(NSDictionary *)payload error:(NSError **)error;
+ (BOOL)validKey:(NSString *)key scope:(NSDictionary *)scope;
+ (BOOL)counter:(id)value;
+ (nullable NSString *)next:(NSString *)local high:(NSString *)high;
@end
NS_ASSUME_NONNULL_END
