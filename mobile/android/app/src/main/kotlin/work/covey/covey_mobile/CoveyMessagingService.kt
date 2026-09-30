package work.covey.covey_mobile

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.RingtoneManager
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

// Push notifications (#424). The instance sends a data message — title,
// body, the agent, the sound, the unread count — and the app shows it
// itself: that way the sound the person chose (#381) plays and a tap opens
// the agent's thread, as on the iPhone.
class CoveyMessagingService : FirebaseMessagingService() {
    override fun onMessageReceived(message: RemoteMessage) {
        val d = message.data
        val title = d["title"] ?: return
        // The agent's thread, or a conversation that is none (#440): Dart
        // reads "conversation:<id>" as the latter.
        val conversation = d["conversation_id"].orEmpty()
        val target = d["agent_id"].orEmpty().ifEmpty { if (conversation.isEmpty()) "" else "conversation:$conversation" }
        Notices.show(
            this, title, d["body"].orEmpty(), target, d["sound"].orEmpty(),
            d["badge"]?.toIntOrNull() ?: 0,
        )
    }

    // Nothing to keep: the app hands its token to the instance on every
    // start, and a new one goes with the next.
    override fun onNewToken(token: String) {}
}

object Notices {
    // The extra a notification's tap carries to MainActivity.
    const val AGENT = "agent_id"

    // The app's sounds as resources, by the name the instance sends without
    // its .caf: covey-<family>-<kind>.
    val sounds: Map<String, Int> = mapOf(
        "covey-bot-question" to R.raw.covey_bot_question,
        "covey-bot-answer" to R.raw.covey_bot_answer,
        "covey-bot-result" to R.raw.covey_bot_result,
        "covey-bot-error" to R.raw.covey_bot_error,
        "covey-schar-question" to R.raw.covey_schar_question,
        "covey-schar-answer" to R.raw.covey_schar_answer,
        "covey-schar-result" to R.raw.covey_schar_result,
        "covey-schar-error" to R.raw.covey_schar_error,
        "covey-glas-question" to R.raw.covey_glas_question,
        "covey-glas-answer" to R.raw.covey_glas_answer,
        "covey-glas-result" to R.raw.covey_glas_result,
        "covey-glas-error" to R.raw.covey_glas_error,
    )

    fun show(context: Context, title: String, body: String, agent: String, sound: String, badge: Int) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        val channel = channel(context, manager, sound.removeSuffix(".caf"))
        val open = PendingIntent.getActivity(
            context, agent.hashCode(),
            Intent(context, MainActivity::class.java).putExtra(AGENT, agent).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val n = NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body.ifEmpty { null })
            .setStyle(if (body.isEmpty()) null else NotificationCompat.BigTextStyle().bigText(body))
            .setContentIntent(open)
            .setAutoCancel(true)
            .setNumber(badge)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setGroup(agent)
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            // Before channels the notification carries its sound itself.
            soundUri(context, sound.removeSuffix(".caf"))?.let { n.setSound(it) }
        }
        manager.notify((System.currentTimeMillis() % Int.MAX_VALUE).toInt(), n.build())
        // The agent's notifications stand together under one entry; the entry
        // itself makes no sound, the notification in it did.
        manager.notify(
            "summary:$agent".hashCode(),
            NotificationCompat.Builder(context, channel)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(title)
                .setContentIntent(open)
                .setAutoCancel(true)
                .setGroup(agent)
                .setGroupSummary(true)
                .setGroupAlertBehavior(NotificationCompat.GROUP_ALERT_CHILDREN)
                .build(),
        )
    }

    // What a sound name plays: one of the app's, the system's, or nothing.
    fun soundUri(context: Context, name: String): Uri? = when (name) {
        "" -> null
        "default" -> RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
        else -> sounds[name]?.let {
            Uri.parse("${ContentResolver.SCHEME_ANDROID_RESOURCE}://${context.packageName}/$it")
        } ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
    }

    // From Android 8 a sound belongs to a channel and not to the
    // notification: one channel per sound, made when first used.
    private fun channel(context: Context, manager: NotificationManager, sound: String): String {
        val id = when {
            sound.isEmpty() -> "silent"
            sound == "default" || sound !in sounds -> "default"
            else -> sound
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && manager.getNotificationChannel(id) == null) {
            val name = when (id) {
                "silent" -> "covey · silent"
                "default" -> "covey · system sound"
                else -> "covey · " + id.removePrefix("covey-").replace('-', ' ')
            }
            val c = NotificationChannel(id, name, NotificationManager.IMPORTANCE_HIGH)
            val attributes = AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_NOTIFICATION)
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                .build()
            c.setSound(if (id == "silent") null else soundUri(context, id), attributes)
            manager.createNotificationChannel(c)
        }
        return id
    }
}
