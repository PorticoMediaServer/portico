#import <Foundation/Foundation.h>
@class PorticoPlaybackController;
NS_ASSUME_NONNULL_BEGIN

/** How a v1 presentation's `audioRender` reaches the render engine (plan §9.3 C3). Swappable: the
 * owner asks only these questions, so the engine and its plan shape can change without touching
 * the timeline, lease or queue plumbing. */
@protocol PorticoListeningRenderBinding <NSObject>
/** Whether this presentation plays through the render engine (else AVPlayer plays its url). */
- (BOOL)renders:(NSDictionary *)presentation;
/** The quiet notice for a presentation that can't render (X-02), or nil. */
- (nullable NSString *)notice:(NSDictionary *)presentation;
/** Whether a prepared presentation (§18.2) can be buffered as the next deck. */
- (BOOL)accepts:(NSDictionary *)presentation origin:(NSString *)origin;
/** The engine command: the current deck, and the prepared or committed successor ({token, presentation}). */
- (nullable NSDictionary *)command:(NSDictionary *)presentation origin:(NSString *)origin settings:(NSDictionary *)settings epoch:(NSInteger)epoch startSeconds:(double)start prepared:(nullable NSDictionary *)prepared committed:(nullable NSDictionary *)committed;
/** The presentation's length from its plan, or 0 when the plan doesn't say. */
- (double)durationSeconds:(NSDictionary *)presentation;
@end

/** The Playback v1 listening owner (plan §9.3 C3): one v1 audio session and its queue. It reports
 * the timeline (holding the lease), pauses and resumes with PATCH, moves the queue with
 * `:advance`, and makes gapless and crossfade edges with `:prepare-next`/`:commit-next`, all while
 * JS sleeps. Main-queue confined. */
@interface PorticoListeningSessionOwner : NSObject
@property(nonatomic,copy,nullable) void (^onUserMutation)(NSDictionary *status);
- (nullable NSDictionary *)stopDisposition;
- (BOOL)consumeStop:(NSString *)key error:(NSError **)error;
@property(nonatomic,copy,nullable) void (^onSnapshot)(NSDictionary *snapshot);
- (instancetype)initWithController:(PorticoPlaybackController *)controller render:(id<PorticoListeningRenderBinding>)render;
- (nullable NSString *)activeOwner;
- (BOOL)adopt:(NSDictionary *)context error:(NSError **)error;
- (BOOL)route:(NSDictionary *)route owner:(NSString *)owner;
- (BOOL)action:(NSString *)action value:(nullable NSNumber *)value owner:(NSString *)owner;
- (BOOL)actionFor:(NSDictionary *)expected action:(NSString *)action value:(nullable NSNumber *)value owner:(NSString *)owner;
- (BOOL)advance:(NSString *)reason entry:(nullable NSString *)entryId owner:(NSString *)owner;
/** The event stream says this session changed: check at once whether the server ended it. */
- (BOOL)sessionUpdated:(NSString *)sessionId owner:(NSString *)owner;
- (BOOL)deadlines:(NSDictionary *)deadlines owner:(NSString *)owner;
- (BOOL)policy:(NSDictionary *)policy owner:(NSString *)owner;
- (BOOL)credentials:(NSString *)token owner:(NSString *)owner;
- (NSDictionary *)snapshot:(NSString *)owner;
- (BOOL)acknowledge:(NSString *)owner sequence:(NSNumber *)sequence;
- (void)metadata:(NSDictionary *)metadata owner:(NSString *)owner;
- (void)skipBack:(double)back forward:(double)forward;
- (void)fact:(NSDictionary *)fact;
- (void)retire:(NSString *)owner stop:(BOOL)stop;
- (void)invalidate;
@end

/** The §18.1 plan version 2: the device decodes the original file (or a FLAC conversion). */
@interface PorticoListeningDecodeRender : NSObject<PorticoListeningRenderBinding>
@end
NS_ASSUME_NONNULL_END
