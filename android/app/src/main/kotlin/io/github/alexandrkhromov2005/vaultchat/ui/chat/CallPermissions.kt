package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.ContextCompat

// ACCESS_LOCAL_NETWORK exists since API 36 (Local Network Protection). Without
// it the system rejects the app's traffic to private addresses, so LAN ICE
// candidates fail. The call still works through a public TURN relay, so only
// the microphone is mandatory.
internal fun callPermissions(sdkInt: Int): List<String> =
    if (sdkInt >= 36) {
        listOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.ACCESS_LOCAL_NETWORK)
    } else {
        listOf(Manifest.permission.RECORD_AUDIO)
    }

internal fun missingCallPermissions(sdkInt: Int, isGranted: (String) -> Boolean): List<String> =
    callPermissions(sdkInt).filterNot(isGranted)

internal fun missingCallPermissions(context: Context): List<String> =
    missingCallPermissions(Build.VERSION.SDK_INT) { permission ->
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
    }

// The microphone is required; a denied local network permission only degrades
// LAN connectivity, so the call may proceed.
internal fun canProceedWithCall(results: Map<String, Boolean>): Boolean =
    results[Manifest.permission.RECORD_AUDIO] != false
