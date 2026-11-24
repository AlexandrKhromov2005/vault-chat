# Retrofit, OkHttp and kotlinx.serialization ship their own consumer rules.

# WebRTC and jni_zero resolve Java entry points from native code by their original names.
-keep class org.webrtc.** { *; }
-keep class org.jni_zero.** { *; }
-dontwarn org.jni_zero.JniZeroJni
