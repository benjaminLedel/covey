package work.covey.covey_mobile

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.media.MediaPlayer
import android.media.RingtoneManager
import android.os.Build
import android.os.Bundle
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    // Push notifications (#424): the permission, the FCM token for the
    // instance, and the thread a tapped notification opens — the same
    // channel as on the iPhone.
    private var push: MethodChannel? = null
    private var pendingPermission: MethodChannel.Result? = null

    // The agent of a notification tapped while the app was not running.
    private var launchAgent: String? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        // Once: not again when the activity is rebuilt, nor when it is
        // started from the recent apps with the old intent.
        if (savedInstanceState == null && intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY == 0) {
            launchAgent = intent.getStringExtra(Notices.AGENT)
        }
        super.onCreate(savedInstanceState)
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        push = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "covey/push").also {
            it.setMethodCallHandler(::handle)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        val agent = intent.getStringExtra(Notices.AGENT) ?: return
        push?.invokeMethod("open", agent) ?: run { launchAgent = agent }
    }

    private fun handle(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "register" -> {
                // Asks once; afterwards the answer stands and only the
                // system's settings change it.
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
                    checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
                ) {
                    pendingPermission?.error("superseded", null, null)
                    pendingPermission = result
                    requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), PERMISSION)
                } else {
                    token(result)
                }
            }
            "launchAgent" -> {
                result.success(launchAgent)
                launchAgent = null
            }
            "preview" -> {
                // Plays a sound as the settings offer it (#381).
                val name = (call.arguments as? String ?: "").removeSuffix(".caf")
                val raw = Notices.sounds[name]
                if (raw != null) {
                    MediaPlayer.create(this, raw)?.apply {
                        setOnCompletionListener { it.release() }
                        start()
                    }
                } else if (name == "default") {
                    RingtoneManager.getRingtone(this, RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION))?.play()
                }
                result.success(null)
            }
            // Android has no number on the icon to set; the notification
            // carries it for launchers that show one.
            "badge" -> result.success(null)
            else -> result.notImplemented()
        }
    }

    // The token for the instance. A build without the app's
    // google-services.json has no Firebase and so no push.
    private fun token(result: MethodChannel.Result) {
        if (FirebaseApp.getApps(this).isEmpty()) {
            return result.error("unavailable", "this build has no Firebase configuration", null)
        }
        FirebaseMessaging.getInstance().token.addOnCompleteListener { t ->
            if (t.isSuccessful && t.result != null) {
                result.success(t.result)
            } else {
                result.error("failed", t.exception?.message, null)
            }
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != PERMISSION) return
        val result = pendingPermission ?: return
        pendingPermission = null
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) {
            token(result)
        } else {
            result.error("denied", null, null)
        }
    }

    private companion object {
        const val PERMISSION = 4240
    }
}
