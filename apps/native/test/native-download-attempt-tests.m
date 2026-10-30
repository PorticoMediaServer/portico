#import <Foundation/Foundation.h>
#import "../Native/PorticoDownloadAttempt.h"

int main(void) {
  @autoreleasepool {
    NSMutableDictionary *entry = [@{@"state": @"downloading", @"taskIdentifier": @22, @"grantUrl": @"https://server/grant/new", @"bytesDone": @0} mutableCopy];
    // All four asynchronous mutation paths (progress, finish, error/resume,
    // policy-cancellation) share this production guard. Deliver an old attempt
    // after the replacement published its identity: no manifest or bytes change.
    for (NSString *callback in @[@"progress", @"finish", @"error-resume", @"wifi-pause"]) {
      NSCAssert(!PorticoDownloadAcceptsTask(entry, 21), @"stale %@ accepted", callback);
    }
    NSCAssert(PorticoDownloadAcceptsTask(entry, 22), @"current task rejected");
    NSData *saved = [NSJSONSerialization dataWithJSONObject:entry options:0 error:nil];
    NSDictionary *reopened = [NSJSONSerialization JSONObjectWithData:saved options:0 error:nil];
    NSCAssert(!PorticoDownloadAcceptsTask(reopened, 21) && PorticoDownloadAcceptsTask(reopened, 22), @"restart lost identity");
    entry[@"state"] = @"ready";
    NSCAssert(!PorticoDownloadAcceptsTask(entry, 22), @"late progress degrades completed file");
    NSCAssert(!PorticoDownloadAcceptsTask(nil, 22), @"removed entry accepts callback");
    NSMutableDictionary *damaged = [@{@"state": @"downloading"} mutableCopy];
    NSCAssert(!PorticoDownloadAcceptsTask(damaged, 21), @"missing task identity accepts callback");
    entry[@"cellularAllowed"] = @YES;
    NSCAssert(PorticoDownloadResumePolicyMatches(entry, YES), @"same cellular policy cannot resume");
    NSCAssert(!PorticoDownloadResumePolicyMatches(entry, NO), @"old cellular request can resume under Wi-Fi only");
    entry[@"cellularAllowed"] = @NO;
    NSCAssert(PorticoDownloadResumePolicyMatches(entry, NO), @"same Wi-Fi policy cannot resume");
    [entry removeObjectForKey:@"cellularAllowed"];
    NSCAssert(!PorticoDownloadResumePolicyMatches(entry, NO), @"unknown resume policy was trusted");
    entry[@"cellularAllowed"] = @1;
    NSCAssert(!PorticoDownloadResumePolicyMatches(entry, YES), @"malformed resume policy was trusted");
    // N12: resume data made under an old address is pointed at the new one, in both the plain
    // and the keyed-archive forms; anything unrecognised is refused (the transfer begins again).
    NSURL *oldURL = [NSURL URLWithString:@"https://server/v1/downloads/old/file"], *newURL = [NSURL URLWithString:@"https://server/v1/downloads/new/file"];
    NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:oldURL];
    [request setValue:@"bytes=1000-" forHTTPHeaderField:@"Range"];
    NSData *archivedRequest = [NSKeyedArchiver archivedDataWithRootObject:request requiringSecureCoding:NO error:nil];
    NSDictionary *info = @{@"NSURLSessionDownloadURL": oldURL.absoluteString, @"NSURLSessionResumeBytesReceived": @1000, @"NSURLSessionResumeCurrentRequest": archivedRequest, @"NSURLSessionResumeOriginalRequest": archivedRequest, @"NSURLSessionResumeEntityTag": @"\"sha256-abc\""};
    NSData *plain = [NSPropertyListSerialization dataWithPropertyList:info format:NSPropertyListBinaryFormat_v1_0 options:0 error:nil];
    NSData *keyed = [NSKeyedArchiver archivedDataWithRootObject:info requiringSecureCoding:NO error:nil];
    for (NSData *resume in @[plain, keyed]) {
      NSData *moved = PorticoDownloadRetargetResume(resume, newURL, NO);
      NSCAssert(moved.length > 0, @"resume data was not moved");
      NSDictionary *read = resume == keyed ? [NSKeyedUnarchiver unarchivedObjectOfClasses:[NSSet setWithArray:@[NSDictionary.class, NSString.class, NSNumber.class, NSData.class]] fromData:moved error:nil] : [NSPropertyListSerialization propertyListWithData:moved options:0 format:NULL error:nil];
      NSCAssert([read[@"NSURLSessionDownloadURL"] isEqual:newURL.absoluteString], @"download address not moved");
      NSCAssert([read[@"NSURLSessionResumeBytesReceived"] isEqual:@1000], @"bytes received lost");
      NSURLRequest *current = PorticoDownloadArchivedRequest(read[@"NSURLSessionResumeCurrentRequest"]);
      NSCAssert([current.URL isEqual:newURL] && [[current valueForHTTPHeaderField:@"Range"] isEqual:@"bytes=1000-"], @"request not moved or its range lost");
      NSCAssert(!current.allowsCellularAccess && !current.allowsExpensiveNetworkAccess, @"Wi-Fi-only choice not written into the saved request");
      NSCAssert([PorticoDownloadArchivedRequest(read[@"NSURLSessionResumeOriginalRequest"]).URL isEqual:newURL], @"original request not moved");
    }
    NSCAssert(PorticoDownloadRetargetResume([@"not a plist" dataUsingEncoding:NSUTF8StringEncoding], newURL, YES) == nil, @"garbage resume data accepted");
    NSCAssert(PorticoDownloadRetargetResume([NSPropertyListSerialization dataWithPropertyList:@{@"other": @1} format:NSPropertyListBinaryFormat_v1_0 options:0 error:nil], newURL, YES) == nil, @"resume data without an address accepted");
    NSLog(@"PASS download attempt fencing: replacement, serialized restart, completed/removed files, missing identity, resume policy, moved address");
  }
}
