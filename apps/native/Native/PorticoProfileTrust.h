#import <Foundation/Foundation.h>
#import <Security/Security.h>
#import <CommonCrypto/CommonCrypto.h>
#import <math.h>

// P10 profile proofs have a distinct Keychain namespace from account credentials,
// selected-server sessions and playback journals. All callers serialize on the
// runtime class, including failed-attempt writes. No PIN or derived key is stored.
static NSString *const PorticoProfileService=@"tv.getportico.portico.profile-trust";
static void PorticoProfileWipe(void *p,size_t n){volatile unsigned char *v=p;while(n--)*v++=0;}
static NSData *PorticoProfileRead(NSString *key,OSStatus *status){
 NSDictionary *q=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:PorticoProfileService,(__bridge id)kSecAttrAccount:key,(__bridge id)kSecReturnData:@YES};
 CFTypeRef value=NULL;*status=SecItemCopyMatching((__bridge CFDictionaryRef)q,&value);id data=CFBridgingRelease(value);
 if(*status!=errSecSuccess)return nil;if(![data isKindOfClass:NSData.class]){*status=errSecDecode;return nil;}return data;
}
static OSStatus PorticoProfileWrite(NSString *key,NSData *data){
 NSDictionary *q=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:PorticoProfileService,(__bridge id)kSecAttrAccount:key};
 if(!data){OSStatus e=SecItemDelete((__bridge CFDictionaryRef)q);if(e!=errSecSuccess&&e!=errSecItemNotFound)return e;OSStatus checked;PorticoProfileRead(key,&checked);return checked==errSecItemNotFound?errSecSuccess:(checked==errSecSuccess?errSecDecode:checked);}
 NSDictionary *v=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};OSStatus e=SecItemUpdate((__bridge CFDictionaryRef)q,(__bridge CFDictionaryRef)v);
 if(e==errSecItemNotFound){NSMutableDictionary *n=[q mutableCopy];[n addEntriesFromDictionary:v];e=SecItemAdd((__bridge CFDictionaryRef)n,NULL);}if(e!=errSecSuccess)return e;
 // A successful API status alone does not qualify a durable PIN-attempt write.
 OSStatus checked;NSData *actual=PorticoProfileRead(key,&checked);return checked==errSecSuccess&&[actual isEqualToData:data]?errSecSuccess:(checked==errSecSuccess?errSecDecode:checked);
}
static NSData *PorticoProfileJSON(id v){return [NSJSONSerialization dataWithJSONObject:v options:0 error:nil];}
static id PorticoProfileParse(NSData *v){return v?[NSJSONSerialization JSONObjectWithData:v options:0 error:nil]:nil;}
static BOOL PorticoProfileText(id v,NSUInteger max){if(![v isKindOfClass:NSString.class]||![v length]||[v length]>max)return NO;return [v rangeOfCharacterFromSet:NSCharacterSet.controlCharacterSet].location==NSNotFound;}
// NSNumber also includes booleans. Corrupt/fractional counters must fail closed,
// not be clamped back to zero and silently reset the durable guessing delay.
static BOOL PorticoProfileAttempts(NSDictionary *record){
 id attempts=record[@"attempts"],until=record[@"lockedUntil"];
 if(![attempts isKindOfClass:NSNumber.class]||![until isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)attempts)==CFBooleanGetTypeID()||CFGetTypeID((__bridge CFTypeRef)until)==CFBooleanGetTypeID())return NO;
 double count=[attempts doubleValue],time=[until doubleValue];return isfinite(count)&&count>=0&&count<=1000&&floor(count)==count&&isfinite(time)&&time>=0;
}
static NSString *PorticoProfileInstallation(OSStatus *e){
 NSData *data=PorticoProfileRead(@"installation",e);if(*e==errSecSuccess){NSString *s=[[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding];if(!PorticoProfileText(s,128))*e=errSecDecode;return *e==errSecSuccess?s:nil;}
 if(*e!=errSecItemNotFound)return nil;NSString *s=NSUUID.UUID.UUIDString;*e=PorticoProfileWrite(@"installation",[s dataUsingEncoding:NSUTF8StringEncoding]);return *e==errSecSuccess?s:nil;
}
static NSData *PorticoProfileDeviceKey(OSStatus *e,BOOL create){
 NSData *data=PorticoProfileRead(@"device-key",e);if(*e==errSecSuccess){if(data.length!=32)*e=errSecDecode;return *e==errSecSuccess?data:nil;}
 if(*e!=errSecItemNotFound||!create)return nil;
 // Never replace the key behind existing proofs. Decryption is read-only; an
 // explicitly emptied vault can generate a new key on its next online save.
 OSStatus checked;NSData *indexData=PorticoProfileRead(@"index.v1",&checked);id index=PorticoProfileParse(indexData);
 if((checked!=errSecItemNotFound&&checked!=errSecSuccess)||(checked==errSecSuccess&&(![index isKindOfClass:NSArray.class]||[index count]!=0))){*e=errSecDecode;return nil;}
 unsigned char bytes[32];*e=SecRandomCopyBytes(kSecRandomDefault,sizeof(bytes),bytes);if(*e!=errSecSuccess)return nil;data=[NSData dataWithBytes:bytes length:sizeof(bytes)];PorticoProfileWipe(bytes,sizeof(bytes));*e=PorticoProfileWrite(@"device-key",data);return *e==errSecSuccess?data:nil;
}
static NSString *PorticoProfileRecordKey(NSString *scope){
 if(!PorticoProfileText(scope,2048))return nil;id parts=PorticoProfileParse([scope dataUsingEncoding:NSUTF8StringEncoding]);
 if(![parts isKindOfClass:NSArray.class]||[parts count]!=6||![parts[0] isEqual:@"portico-profile-trust-v1"]||![@[@"hosted",@"local"]containsObject:parts[1]])return nil;
 for(NSUInteger i=2;i<6;i++)if(!PorticoProfileText(parts[i],128))return nil;
 NSData *data=[scope dataUsingEncoding:NSUTF8StringEncoding];unsigned char hash[CC_SHA256_DIGEST_LENGTH];CC_SHA256(data.bytes,(CC_LONG)data.length,hash);NSMutableString *key=[@"profile:" mutableCopy];for(NSUInteger i=0;i<sizeof(hash);i++)[key appendFormat:@"%02x",hash[i]];return key;
}
static BOOL PorticoProfilePIN(NSString *pin,BOOL required){
 if(![pin isKindOfClass:NSString.class])return NO;if(!required)return pin.length==0;if(pin.length!=4)return NO;
 for(NSUInteger i=0;i<4;i++)if([pin characterAtIndex:i]<'0'||[pin characterAtIndex:i]>'9')return NO;return YES;
}
static BOOL PorticoProfileKeys(NSString *scope,NSString *pin,NSData *salt,unsigned char keys[64],OSStatus *e,BOOL create){
 NSData *device=PorticoProfileDeviceKey(e,create);if(!device)return NO;NSMutableData *material=[NSMutableData dataWithData:[pin dataUsingEncoding:NSUTF8StringEncoding]];[material appendData:device];[material appendData:[scope dataUsingEncoding:NSUTF8StringEncoding]];
 int status=CCKeyDerivationPBKDF(kCCPBKDF2,material.bytes,material.length,salt.bytes,salt.length,kCCPRFHmacAlgSHA256,200000,keys,64);PorticoProfileWipe(material.mutableBytes,material.length);if(status!=kCCSuccess){*e=errSecInternalComponent;return NO;}return YES;
}
static NSData *PorticoProfileMAC(NSString *scope,NSString *metadata,NSData *salt,NSData *iv,NSData *cipher,const unsigned char *key){
 CCHmacContext ctx;CCHmacInit(&ctx,kCCHmacAlgSHA256,key,32);for(NSData *part in @[[scope dataUsingEncoding:NSUTF8StringEncoding],[metadata dataUsingEncoding:NSUTF8StringEncoding],salt,iv,cipher]){uint64_t n=CFSwapInt64HostToBig(part.length);CCHmacUpdate(&ctx,&n,sizeof(n));CCHmacUpdate(&ctx,part.bytes,part.length);}unsigned char tag[CC_SHA256_DIGEST_LENGTH];CCHmacFinal(&ctx,tag);NSData *out=[NSData dataWithBytes:tag length:sizeof(tag)];PorticoProfileWipe(tag,sizeof(tag));PorticoProfileWipe(&ctx,sizeof(ctx));return out;
}
static BOOL PorticoProfileEqual(NSData *a,NSData *b){if(a.length!=b.length||a.length!=CC_SHA256_DIGEST_LENGTH)return NO;const unsigned char *x=a.bytes,*y=b.bytes;unsigned char d=0;for(NSUInteger i=0;i<a.length;i++)d|=x[i]^y[i];return d==0;}
static NSArray *PorticoProfileIndex(OSStatus *e){NSData *raw=PorticoProfileRead(@"index.v1",e);if(*e==errSecItemNotFound){*e=errSecSuccess;return @[];}id v=PorticoProfileParse(raw);if(*e!=errSecSuccess)return nil;if(![v isKindOfClass:NSArray.class]||[v count]>64){*e=errSecDecode;return nil;}for(id k in v)if(!PorticoProfileText(k,80)||![k hasPrefix:@"profile:"]){*e=errSecDecode;return nil;}return v;}
// Expired entries must not fill an invisible, unremovable 64-profile vault.
// Pruning only removes proof material; malformed records still fail closed.
// Callers hold PorticoRuntime's lock throughout read, deletion and index readback.
static NSArray *PorticoProfileActiveIndex(OSStatus *e){
 NSArray *index=PorticoProfileIndex(e);if(!index)return nil;NSMutableArray *next=[NSMutableArray array];double now=NSDate.date.timeIntervalSince1970*1000;
 for(NSString *key in index){
  NSData *raw=PorticoProfileRead(key,e);if(*e==errSecItemNotFound){*e=errSecSuccess;continue;}if(*e!=errSecSuccess)return nil;
  id record=PorticoProfileParse(raw);if(![record isKindOfClass:NSDictionary.class]||!PorticoProfileText(record[@"metadata"],8192)){*e=errSecDecode;return nil;}
  id metadata=PorticoProfileParse([record[@"metadata"]dataUsingEncoding:NSUTF8StringEncoding]);id expires=[metadata isKindOfClass:NSDictionary.class]?metadata[@"expiresAtMs"]:nil;
  if(![expires isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)expires)==CFBooleanGetTypeID()||!isfinite([expires doubleValue])||[expires doubleValue]<=0){*e=errSecDecode;return nil;}
  if([expires doubleValue]<=now){*e=PorticoProfileWrite(key,nil);if(*e!=errSecSuccess)return nil;}else [next addObject:key];
 }
 if(next.count!=index.count){*e=PorticoProfileWrite(@"index.v1",PorticoProfileJSON(next));if(*e!=errSecSuccess)return nil;}
 return next;
}
static BOOL PorticoProfileMetadata(NSString *scope,id metadata,NSString *installation){
 id parts=PorticoProfileParse([scope dataUsingEncoding:NSUTF8StringEncoding]);if(![metadata isKindOfClass:NSDictionary.class]||![parts isKindOfClass:NSArray.class]||[parts count]!=6)return NO;
 if(![metadata[@"authority"]isEqual:parts[1]]||![metadata[@"accountId"]isEqual:parts[2]]||![metadata[@"serverId"]isEqual:parts[3]]||![metadata[@"profileId"]isEqual:parts[4]]||![metadata[@"installationId"]isEqual:parts[5]]||![metadata[@"installationId"]isEqual:installation])return NO;
 if(!PorticoProfileText(metadata[@"profileName"],120)||!PorticoProfileText(metadata[@"serverName"],2048)||!PorticoProfileText(metadata[@"serverUrl"],4096)||![metadata[@"pinRequired"]isKindOfClass:NSNumber.class]||![metadata[@"expiresAtMs"]isKindOfClass:NSNumber.class])return NO;
 double now=NSDate.date.timeIntervalSince1970*1000,expires=[metadata[@"expiresAtMs"]doubleValue];return isfinite(expires)&&expires>now&&expires<=now+30.0*86400000+60000;
}
static NSDictionary *PorticoProfileEnvelope(NSString *scope,NSString *metadataJSON,NSString *payloadJSON,NSString *pin,OSStatus *e){
 NSData *plain=[payloadJSON dataUsingEncoding:NSUTF8StringEncoding];if(!plain||plain.length>32768||![PorticoProfileParse(plain)isKindOfClass:NSDictionary.class]){*e=errSecParam;return nil;}
 unsigned char saltBytes[32],ivBytes[kCCBlockSizeAES128],keys[64];*e=SecRandomCopyBytes(kSecRandomDefault,sizeof(saltBytes),saltBytes);if(*e==errSecSuccess)*e=SecRandomCopyBytes(kSecRandomDefault,sizeof(ivBytes),ivBytes);if(*e!=errSecSuccess)return nil;
 NSData *salt=[NSData dataWithBytes:saltBytes length:sizeof(saltBytes)],*iv=[NSData dataWithBytes:ivBytes length:sizeof(ivBytes)];if(!PorticoProfileKeys(scope,pin,salt,keys,e,YES))return nil;
 NSMutableData *cipher=[NSMutableData dataWithLength:plain.length+kCCBlockSizeAES128];size_t written=0;CCCryptorStatus status=CCCrypt(kCCEncrypt,kCCAlgorithmAES,kCCOptionPKCS7Padding,keys,32,iv.bytes,plain.bytes,plain.length,cipher.mutableBytes,cipher.length,&written);
 if(status!=kCCSuccess){PorticoProfileWipe(keys,sizeof(keys));*e=errSecInternalComponent;return nil;}cipher.length=written;NSData *mac=PorticoProfileMAC(scope,metadataJSON,salt,iv,cipher,keys+32);PorticoProfileWipe(keys,sizeof(keys));
 return @{@"version":@1,@"scope":scope,@"metadata":metadataJSON,@"salt":[salt base64EncodedStringWithOptions:0],@"iv":[iv base64EncodedStringWithOptions:0],@"cipher":[cipher base64EncodedStringWithOptions:0],@"mac":[mac base64EncodedStringWithOptions:0],@"attempts":@0,@"lockedUntil":@0};
}
static NSString *PorticoProfileDecrypt(NSDictionary *record,NSString *scope,NSString *pin,OSStatus *e){
 if(![record isKindOfClass:NSDictionary.class]||![record[@"version"]isEqual:@1]||![record[@"scope"]isEqual:scope]||!PorticoProfileText(record[@"metadata"],8192)){*e=errSecDecode;return nil;}
 for(NSString *key in @[@"salt",@"iv",@"cipher",@"mac"])if(!PorticoProfileText(record[key],65536)){*e=errSecDecode;return nil;}
 NSData *salt=[[NSData alloc]initWithBase64EncodedString:record[@"salt"] options:0],*iv=[[NSData alloc]initWithBase64EncodedString:record[@"iv"] options:0],*cipher=[[NSData alloc]initWithBase64EncodedString:record[@"cipher"] options:0],*mac=[[NSData alloc]initWithBase64EncodedString:record[@"mac"] options:0];
 if(salt.length!=32||iv.length!=kCCBlockSizeAES128||cipher.length<16||cipher.length>32784||mac.length!=32){*e=errSecDecode;return nil;}
 unsigned char keys[64];if(!PorticoProfileKeys(scope,pin,salt,keys,e,NO))return nil;NSData *expected=PorticoProfileMAC(scope,record[@"metadata"],salt,iv,cipher,keys+32);
 if(!PorticoProfileEqual(mac,expected)){PorticoProfileWipe(keys,sizeof(keys));*e=errSecAuthFailed;return nil;}
 NSMutableData *plain=[NSMutableData dataWithLength:cipher.length];size_t written=0;CCCryptorStatus status=CCCrypt(kCCDecrypt,kCCAlgorithmAES,kCCOptionPKCS7Padding,keys,32,iv.bytes,cipher.bytes,cipher.length,plain.mutableBytes,plain.length,&written);PorticoProfileWipe(keys,sizeof(keys));
 if(status!=kCCSuccess){*e=errSecDecode;PorticoProfileWipe(plain.mutableBytes,plain.length);return nil;}plain.length=written;NSString *text=[[NSString alloc]initWithData:plain encoding:NSUTF8StringEncoding];if(![PorticoProfileParse(plain)isKindOfClass:NSDictionary.class])*e=errSecDecode;PorticoProfileWipe(plain.mutableBytes,plain.length);return *e==errSecSuccess?text:nil;
}
