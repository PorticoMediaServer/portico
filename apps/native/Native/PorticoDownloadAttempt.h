#import <Foundation/Foundation.h>

// Called on the downloads serial queue before any callback mutates manifest or
// resume bytes. A callback must identify the exact current attempt. A damaged
// manifest with no task identity cannot authorize a late callback.
static inline BOOL PorticoDownloadAcceptsTask(NSDictionary *entry, NSUInteger taskIdentifier) {
  if (!entry || [entry[@"state"] isEqual:@"ready"]) return NO;
  NSNumber *current = entry[@"taskIdentifier"];
  return [current isKindOfClass:NSNumber.class] && current.unsignedIntegerValue == taskIdentifier;
}

// Resume data preserves the original request's policy; it cannot be tightened on reuse.
static inline BOOL PorticoDownloadResumePolicyMatches(NSDictionary *entry, BOOL cellular) {
  id policy = entry[@"cellularAllowed"];
  return [policy isKindOfClass:NSNumber.class] && CFGetTypeID((__bridge CFTypeRef)policy) == CFBooleanGetTypeID() && [policy boolValue] == cellular;
}

// N12: a transfer whose address had to change (an address renewed after more than a day
// untouched, or a claim made again) continues from the bytes the system already has. Resume data
// carries the address of the request it was made from, and the system offers no way to change
// it, so the address is changed inside the data: the download's address and the two archived
// requests it holds. The server answers a range of the same file under any address for its
// claim (the file's ETag is its SHA-256, so If-Range holds). Nil when the data is not in a form
// this knows; the transfer then begins again, as before.
static inline NSURLRequest *PorticoDownloadArchivedRequest(NSData *archived) {
  NSError *error = nil;
  NSKeyedUnarchiver *reader = [[NSKeyedUnarchiver alloc] initForReadingFromData:archived error:&error];
  if (!reader) return nil;
  reader.requiresSecureCoding = NO;
  id request = [reader decodeObjectForKey:NSKeyedArchiveRootObjectKey];
  // Some system versions archived it under the constant's name rather than its value.
  if (!request) request = [reader decodeObjectForKey:@"NSKeyedArchiveRootObjectKey"];
  [reader finishDecoding];
  return [request isKindOfClass:NSURLRequest.class] ? request : nil;
}

// The archived requests also carry the current mobile-data choice, so a changed setting keeps
// the partial file (JUSTIN-ANSWERS #17) instead of replaying the old policy.
static inline NSData *PorticoDownloadRetargetResume(NSData *resume, NSURL *url, BOOL cellular) {
  if (resume.length == 0 || !url) return nil;
  @try {
    NSError *error = nil;
    id top = [NSPropertyListSerialization propertyListWithData:resume options:NSPropertyListImmutable format:NULL error:&error];
    if (![top isKindOfClass:NSDictionary.class]) return nil;
    BOOL keyed = ((NSDictionary *)top)[@"$archiver"] != nil;
    NSMutableDictionary *info = nil;
    if (keyed) {
      NSKeyedUnarchiver *reader = [[NSKeyedUnarchiver alloc] initForReadingFromData:resume error:&error];
      if (!reader) return nil;
      reader.requiresSecureCoding = NO;
      id root = [reader decodeObjectForKey:NSKeyedArchiveRootObjectKey];
      [reader finishDecoding];
      if (![root isKindOfClass:NSDictionary.class]) return nil;
      info = [root mutableCopy];
    } else {
      info = [top mutableCopy];
    }
    if (![info[@"NSURLSessionDownloadURL"] isKindOfClass:NSString.class]) return nil;
    info[@"NSURLSessionDownloadURL"] = url.absoluteString;
    for (NSString *key in @[@"NSURLSessionResumeCurrentRequest", @"NSURLSessionResumeOriginalRequest"]) {
      id archived = info[key];
      if (!archived) continue;
      if (![archived isKindOfClass:NSData.class]) return nil;
      NSURLRequest *request = PorticoDownloadArchivedRequest(archived);
      if (!request) return nil;
      NSMutableURLRequest *moved = [request mutableCopy];
      moved.URL = url;
      moved.allowsCellularAccess = cellular;
      moved.allowsExpensiveNetworkAccess = cellular;
      NSData *again = [NSKeyedArchiver archivedDataWithRootObject:moved requiringSecureCoding:NO error:&error];
      if (!again) return nil;
      info[key] = again;
    }
    if (keyed) return [NSKeyedArchiver archivedDataWithRootObject:info requiringSecureCoding:NO error:&error];
    return [NSPropertyListSerialization dataWithPropertyList:info format:NSPropertyListBinaryFormat_v1_0 options:0 error:&error];
  } @catch (NSException *exception) {
    return nil;
  }
}
