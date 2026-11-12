plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
}

// Release signing material lives outside the repository: in
// ~/.gradle/gradle.properties or the environment (see android/README.md).
fun signingValue(property: String, environment: String): String? =
    providers.gradleProperty(property).orElse(providers.environmentVariable(environment)).orNull

val releaseStoreFile = signingValue("vaultchat.signing.storeFile", "VAULTCHAT_SIGNING_STORE_FILE")

// The server offered on the sign-in screen. The repository carries no
// deployment address; set vaultchat.defaultServer locally to prefill it.
// It is pasted into Java source, so only a plain scheme://host[:port] passes.
val defaultServer = providers.gradleProperty("vaultchat.defaultServer").orElse("").get().also {
    require(it.isEmpty() || Regex("https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?/?").matches(it)) {
        "vaultchat.defaultServer must look like https://host[:port]"
    }
}

// The Firebase project for push (docs/android/push.md), kept outside the
// repository like the signing key. Without it the build has no push.
fun firebaseValue(name: String): String = providers.gradleProperty("vaultchat.firebase.$name").orElse("").get().also {
    require(Regex("[A-Za-z0-9:._-]*").matches(it)) { "vaultchat.firebase.$name has unexpected characters" }
}

// All or none: Firebase started without one of them may throw in
// Application.onCreate, which would crash every start of the app.
val firebase = listOf("appId", "apiKey", "projectId", "senderId").associateWith(::firebaseValue).also { values ->
    val missing = values.filterValues { it.isEmpty() }.keys
    require(missing.isEmpty() || missing.size == values.size) {
        "set all of vaultchat.firebase.* or none of them; missing: ${missing.joinToString()}"
    }
}

android {
    namespace = "io.github.alexandrkhromov2005.vaultchat"
    compileSdk = 37

    defaultConfig {
        // The application ID and the signing key identify the app for its
        // whole life: changing either makes updates fail to install.
        applicationId = "io.github.alexandrkhromov2005.vaultchat"
        minSdk = 26
        targetSdk = 37
        versionCode = 2
        versionName = "0.1.1"

        buildConfigField("String", "FIREBASE_APP_ID", "\"${firebase.getValue("appId")}\"")
        buildConfigField("String", "FIREBASE_API_KEY", "\"${firebase.getValue("apiKey")}\"")
        buildConfigField("String", "FIREBASE_PROJECT_ID", "\"${firebase.getValue("projectId")}\"")
        buildConfigField("String", "FIREBASE_SENDER_ID", "\"${firebase.getValue("senderId")}\"")
    }

    signingConfigs {
        if (releaseStoreFile != null) {
            create("release") {
                storeFile = file(releaseStoreFile)
                storePassword = signingValue("vaultchat.signing.storePassword", "VAULTCHAT_SIGNING_STORE_PASSWORD")
                keyAlias = signingValue("vaultchat.signing.keyAlias", "VAULTCHAT_SIGNING_KEY_ALIAS")
                keyPassword = signingValue("vaultchat.signing.keyPassword", "VAULTCHAT_SIGNING_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        debug {
            applicationIdSuffix = ".debug"
            // The emulator reaches the host's Compose stack at 10.0.2.2.
            buildConfigField("String", "DEFAULT_SERVER", "\"${defaultServer.ifEmpty { "http://10.0.2.2:8080" }}\"")
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.findByName("release")
            buildConfigField("String", "DEFAULT_SERVER", "\"$defaultServer\"")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    // The APK is distributed outside Google Play; the dependency metadata
    // block is only readable by Google and is not needed.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }

    testOptions {
        // Robolectric runs the Room tests on the JVM with Android resources.
        unitTests.isIncludeAndroidResources = true
    }

    lint {
        abortOnError = true
        checkReleaseBuilds = true
    }
}

// The schema of every database version is kept for migration tests.
ksp {
    arg("room.schemaLocation", "$projectDir/schemas")
}

dependencies {
    implementation(platform(libs.compose.bom))
    implementation(libs.androidx.core)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.process)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.okhttp)
    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.room.runtime)
    implementation(libs.room.paging)
    implementation(libs.work.runtime)
    implementation(libs.paging.runtime)
    implementation(libs.paging.compose)
    implementation(libs.coil.compose)
    implementation(libs.firebase.messaging)
    implementation(libs.kotlinx.coroutines.play.services)
    ksp(libs.room.compiler)
    debugImplementation(libs.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.work.testing)
    testImplementation(platform(libs.compose.bom))
    testImplementation(libs.compose.ui.test.junit4)
}
