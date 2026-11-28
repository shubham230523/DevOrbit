import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// ---------------------------------------------------------------------------------------------
// Release signing configuration
//
// Credentials are NEVER committed. They are resolved, in order, from:
//   1. Environment variables (preferred for CI):
//        DEVORBIT_ANDROID_KEYSTORE_PATH, DEVORBIT_ANDROID_KEYSTORE_PASSWORD,
//        DEVORBIT_ANDROID_KEY_ALIAS, DEVORBIT_ANDROID_KEY_PASSWORD
//   2. android/key.properties (developer machine, git-ignored; see key.properties.example)
//
// A release build FAILS CLOSED when credentials are absent: it is never silently signed with the
// debug keystore (see `verifyReleaseSigning` below).
// ---------------------------------------------------------------------------------------------
val keystorePropertiesFile = rootProject.file("key.properties")
val keystoreProperties = Properties().apply {
    if (keystorePropertiesFile.exists()) {
        FileInputStream(keystorePropertiesFile).use { load(it) }
    }
}

fun signingValue(envKey: String, propertyKey: String): String? {
    val fromEnv = System.getenv(envKey)?.trim()
    if (!fromEnv.isNullOrEmpty()) return fromEnv
    val fromProperty = keystoreProperties.getProperty(propertyKey)?.trim()
    return if (fromProperty.isNullOrEmpty()) null else fromProperty
}

val releaseStoreFilePath = signingValue("DEVORBIT_ANDROID_KEYSTORE_PATH", "storeFile")
val releaseStorePassword = signingValue("DEVORBIT_ANDROID_KEYSTORE_PASSWORD", "storePassword")
val releaseKeyAlias = signingValue("DEVORBIT_ANDROID_KEY_ALIAS", "keyAlias")
val releaseKeyPassword = signingValue("DEVORBIT_ANDROID_KEY_PASSWORD", "keyPassword")

val hasReleaseSigning = !releaseStoreFilePath.isNullOrEmpty() &&
    !releaseStorePassword.isNullOrEmpty() &&
    !releaseKeyAlias.isNullOrEmpty() &&
    !releaseKeyPassword.isNullOrEmpty()

// Resolve the keystore relative to the android/ project directory when not absolute.
val resolvedReleaseStoreFile: File? = releaseStoreFilePath?.let { path ->
    val candidate = File(path)
    if (candidate.isAbsolute) candidate else rootProject.file(path)
}

android {
    namespace = "com.devorbit.devorbit_mobile"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // Stable application id: changing it later breaks Play updates and pairing storage.
        applicationId = "com.devorbit.devorbit_mobile"
        // Android 7.0+: required baseline for the modern TLS stack used by the broker/E2E channel.
        minSdk = maxOf(flutter.minSdkVersion, 24)
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        if (hasReleaseSigning && resolvedReleaseStoreFile != null) {
            create("release") {
                storeFile = resolvedReleaseStoreFile
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
                enableV1Signing = false
                enableV2Signing = true
                enableV3Signing = true
                enableV4Signing = true
            }
        }
    }

    buildTypes {
        debug {
            ndk {
                abiFilters.clear()
                abiFilters.add("arm64-v8a")
            }
            packaging {
                jniLibs {
                    useLegacyPackaging = true
                    excludes += setOf("**/armeabi-v7a/**", "**/x86_64/**", "**/x86/**")
                }
            }
        }
        release {
            // R8 shrinking is intentionally left off until a release build can be validated on a
            // real device. Turning it on without runtime verification risks stripping reflectively
            // loaded plugin classes. Rules are prepared in proguard-rules.pro for that step.
            isMinifyEnabled = false
            isShrinkResources = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
            // Deliberately NOT falling back to the debug keystore: when credentials are missing the
            // signing config is absent and the task-graph guard below fails the build.
            signingConfig = if (hasReleaseSigning) signingConfigs.findByName("release") else null
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

// Fail closed for every release artifact: assembling/bundling a release requires real credentials.
val releaseArtifactTaskNames = setOf(
    "assembleRelease",
    "bundleRelease",
    "packageRelease",
    "packageReleaseBundle",
    "signReleaseBundle",
    "assembleReleaseBundle",
)

gradle.taskGraph.whenReady {
    val requestedReleaseTasks = allTasks
        .map { it.name }
        .filter { releaseArtifactTaskNames.contains(it) }
    if (requestedReleaseTasks.isNotEmpty() && !hasReleaseSigning) {
        throw GradleException(
            "DevOrbit release build refused: no release signing credentials configured.\n" +
                "Provide DEVORBIT_ANDROID_KEYSTORE_PATH / DEVORBIT_ANDROID_KEYSTORE_PASSWORD / " +
                "DEVORBIT_ANDROID_KEY_ALIAS / DEVORBIT_ANDROID_KEY_PASSWORD environment variables " +
                "or an untracked android/key.properties file.\n" +
                "Release builds are never signed with the debug keystore. " +
                "See apps/mobile/android/KEYSTORE_SETUP.md. Requested: $requestedReleaseTasks"
        )
    }
}
