# DevOrbit release R8/ProGuard rules.
#
# Flutter plugins used by the app already ship consumer rules inside their AARs, so this file only
# adds defensive keeps for reflectively-discovered entry points. Keep the list minimal: broad keeps
# defeat shrinking and inflate the release artifact.

-keepattributes *Annotation*

# Platform-channel plugin registrants and their handlers are looked up by name.
-keep class com.it_nomads.fluttersecurestorage.** { *; }
-keep class dev.steenbakker.mobile_scanner.** { *; }
