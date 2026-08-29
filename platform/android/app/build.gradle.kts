plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// --- Versioning: derived from git so releases need no manual edits -----------
// versionName comes from the latest tag (v1.2.3 -> "1.2.3"); versionCode is the
// commit count (monotonic, so each release increments). Fallbacks keep local /
// untagged / no-git builds working. CI must checkout with full history
// (fetch-depth: 0) for the commit count to be correct.
fun git(vararg args: String): String? = try {
    val proc = ProcessBuilder(listOf("git") + args)
        .directory(rootDir)
        .redirectErrorStream(true)
        .start()
    val out = proc.inputStream.bufferedReader().readText().trim()
    if (proc.waitFor() == 0 && out.isNotEmpty()) out else null
} catch (e: Exception) {
    null
}

val gitVersionName: String =
    git("describe", "--tags", "--abbrev=0")?.removePrefix("v") ?: "0.0.0"
val gitVersionCode: Int =
    System.getenv("WESYNC_VERSION_CODE")?.toIntOrNull()
        ?: git("rev-list", "--count", "HEAD")?.toIntOrNull() ?: 1

android {
    namespace = "com.wesync.app"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.wesync.app"
        // minSdk = 21 matches gomobile's lowest supported API. The AAR was
        // built with -androidapi=21; raising minSdk above 21 is fine, but
        // never below.
        minSdk = 21
        targetSdk = 34
        versionCode = gitVersionCode
        versionName = gitVersionName
        // arm64-v8a only. (x86_64 was tried for emulator testing but Go's
        // amd64 runtime hits Android's seccomp lstat block on API 26+, so an
        // x86_64 build can't run on a modern emulator anyway — and shipping it
        // would crash on x86_64 devices. Real devices are arm64.)
        ndk {
            abiFilters += listOf("arm64-v8a")
        }
    }

    // Release signing reads the keystore from environment variables (wired to
    // GitHub Secrets in CI, or `podman run -e ...` locally). When KEYSTORE_PATH
    // is absent — local debug builds, or CI without secrets — we fall back to
    // the debug key so the build still works; a real release just requires the
    // env vars to be set.
    val releaseStorePath: String? = System.getenv("KEYSTORE_PATH")
    signingConfigs {
        if (releaseStorePath != null) {
            create("release") {
                storeFile = file(releaseStorePath)
                storePassword = System.getenv("KEYSTORE_PASSWORD")
                keyAlias = System.getenv("KEY_ALIAS")
                keyPassword = System.getenv("KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = if (releaseStorePath != null) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }

    testOptions {
        // Robolectric needs the merged manifest + resources to stand up a fake
        // Android runtime for the WorkManager scheduling tests (SyncSchedulerTest).
        unitTests.isIncludeAndroidResources = true
    }

    packaging {
        // We exec libsyncthing.so from Go via exec.Command, which needs the
        // file to physically exist on disk in the app's nativeLibraryDir.
        // With useLegacyPackaging = false (the AGP default), Android keeps
        // .so files inside the APK and only memory-maps them for
        // System.loadLibrary — there is no path to point exec() at. Forcing
        // legacy packaging extracts them at install time as real files.
        jniLibs.useLegacyPackaging = true
    }

    applicationVariants.all {
        val variant = this
        val publish = tasks.register("publish${variant.name.replaceFirstChar { it.uppercase() }}Apk") {
            // Copies the freshly-built APK into <repo>/dist/android/ so it
            // lives alongside dist/windows/ instead of being buried under
            // platform/android/app/build/outputs/. Renames it to
            // wesync-<variant>.apk so multiple builds (debug/release) coexist
            // without clobber. The project now lives at platform/android/, so
            // the repo root is two levels up (../../) not one.
            dependsOn("assemble${variant.name.replaceFirstChar { it.uppercase() }}")
            val distDir = rootProject.layout.projectDirectory.dir("../../dist/android")
            doLast {
                val apk = variant.outputs.first().outputFile
                if (!apk.exists()) {
                    throw GradleException("APK not found at $apk")
                }
                val target = distDir.asFile
                target.mkdirs()
                val dest = target.resolve("wesync-${variant.name}.apk")
                apk.copyTo(dest, overwrite = true)
                println("Published APK → ${dest.absolutePath}")
            }
        }
        // Hook into the standard build chain so any `./gradlew assemble*`
        // automatically deposits the APK into dist/.
        tasks.named("assemble${variant.name.replaceFirstChar { it.uppercase() }}").configure {
            finalizedBy(publish)
        }
    }
}

dependencies {
    // Our Go backend, compiled via gomobile bind.
    implementation(files("libs/wesync.aar"))

    // Standard AndroidX baseline. WebView is in the platform; we just need
    // AppCompatActivity as the container.
    implementation("androidx.appcompat:appcompat:1.8.0")

    // WorkManager owns all background-sync scheduling (Doze-aware, survives
    // reboot, runs the sync as a long-running foreground worker). The -ktx
    // artifact pulls in kotlinx-coroutines, which CoroutineWorker needs.
    //
    // Pinned to 2.9.1: work 2.10+ requires compileSdk 35 + AGP 8.6.0, a
    // toolchain migration we're deferring. Keep work-testing below in lockstep.
    implementation("androidx.work:work-runtime-ktx:2.9.1")

    // JVM unit tests for the pure power logic (PowerLogic.kt). These run on
    // the local JVM — no device, no Robolectric. The android.jar shipped to
    // unit tests stubs org.json to throw "Stub!", so we add the real
    // implementation explicitly; on the test classpath it shadows the stub.
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20231013")

    // Robolectric + work-testing let the WorkManager scheduling glue
    // (SyncScheduler.enqueueFromPlan → real enqueue/cancel) run on the local
    // JVM with a fake Android runtime — no device. This covers the wiring that
    // PowerLogicTest's pure planWorks can't: that each wake-plan mode actually
    // arms the right unique work and a mode switch cancels the stale ones.
    // work-testing brings SynchronousExecutor + WorkManagerTestInitHelper.
    testImplementation("org.robolectric:robolectric:4.11.1")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("androidx.work:work-testing:2.9.1")
}
