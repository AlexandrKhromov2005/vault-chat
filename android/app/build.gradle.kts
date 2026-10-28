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

android {
    namespace = "io.github.alexandrkhromov2005.vaultchat"
    compileSdk = 37

    defaultConfig {
        // The application ID and the signing key identify the app for its
        // whole life: changing either makes updates fail to install.
        applicationId = "io.github.alexandrkhromov2005.vaultchat"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.1.0"
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
    ksp(libs.room.compiler)
    debugImplementation(libs.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.core)
    testImplementation(libs.work.testing)
}
