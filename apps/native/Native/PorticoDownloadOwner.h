#import <Foundation/Foundation.h>

// BETA06: the app-wide owner of iPhone and iPad downloads (PorticoDownloads.m). Creating it
// creates the background session, so it is started natively and in an order the system
// requires: on a background relaunch only once the session's completion handler is held, and
// otherwise when a scene comes to the foreground (or React Native first uses it). Whether React
// Native starts makes no difference to the session.
@interface PorticoDownloadOwner : NSObject
// The background session's identifier; the system reconnects tasks only to this one.
@property (class, nonatomic, readonly) NSString *sessionIdentifier;
// Main queue. Creates the owner (and the session) if needed and tries again to read a manifest
// that could not be read yet. Safe to call at every wake.
+ (void)start;
@end
