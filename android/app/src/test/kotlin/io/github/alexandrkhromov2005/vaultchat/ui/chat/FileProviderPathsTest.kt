package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.content.Context
import android.content.pm.PackageManager
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.xmlpull.v1.XmlPullParser

/**
 * Other apps can be handed downloaded attachments and nothing else of the
 * app. FileProvider itself compares paths with "/", so it cannot be run on a
 * Windows build machine; its configuration is checked instead.
 */
@RunWith(RobolectricTestRunner::class)
class FileProviderPathsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    @Test
    fun onlyTheAttachmentCacheIsServed() {
        val roots = mutableListOf<String>()
        val parser = context.resources.getXml(R.xml.file_paths)
        while (parser.next() != XmlPullParser.END_DOCUMENT) {
            if (parser.eventType == XmlPullParser.START_TAG && parser.name != "paths") {
                roots += "${parser.name} ${parser.getAttributeValue(null, "path")}"
            }
        }
        // AttachmentCache's root; staged copies, the database and the session live elsewhere.
        assertEquals(listOf("cache-path attachments/"), roots)
    }

    @Test
    fun theProviderIsNotExported() {
        val provider = context.packageManager.resolveContentProvider("${context.packageName}.files", PackageManager.GET_META_DATA)!!
        assertFalse(provider.exported)
        assertEquals(R.xml.file_paths, provider.metaData.getInt("android.support.FILE_PROVIDER_PATHS"))
    }
}
