# R8 keep rules for lolad's release APK.
# The UI is built programmatically, so only the libraries that use reflection or
# service loading need explicit keeps.

-keepattributes Signature,InnerClasses,EnclosingMethod,*Annotation*

# OkHttp / Okio: optional TLS providers and Kotlin metadata lookups.
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
-dontwarn org.slf4j.**
-keep class okhttp3.internal.platform.** { *; }
-keep class okhttp3.internal.publicsuffix.** { *; }

# Markwon builds spans from plugin classes and can be reached by reflection.
-keep class io.noties.markwon.** { *; }
-dontwarn io.noties.markwon.**

# ZXing embedded scanner inflates its capture activity from the merged manifest
# and resolves decoder classes by name.
-keep class com.journeyapps.barcodescanner.** { *; }
-keep class com.google.zxing.** { *; }
-dontwarn com.google.zxing.**

# JSON is a platform API, but keep the entry points that R8 may otherwise strip
# when only reached through reflection inside third-party code.
-keepclassmembers class * implements android.os.Parcelable {
  public static final ** CREATOR;
}
