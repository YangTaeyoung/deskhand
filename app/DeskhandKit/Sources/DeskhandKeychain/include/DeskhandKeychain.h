#pragma once
#include <Security/Security.h>

OSStatus DeskhandKeychainCopyMatching(CFDictionaryRef query, CFTypeRef *result);
OSStatus DeskhandKeychainAdd(CFDictionaryRef attributes);
OSStatus DeskhandKeychainUpdate(CFDictionaryRef query, CFDictionaryRef attributes);
OSStatus DeskhandKeychainDelete(CFDictionaryRef query);
